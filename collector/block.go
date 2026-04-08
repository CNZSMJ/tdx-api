package collector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"xorm.io/xorm"
)

var blockFiles = []string{
	"block.dat",
	"block_gn.dat",
	"block_fg.dat",
	"block_zs.dat",
}

type BlockConfig struct {
	BaseDir string
	Now     func() time.Time
}

// blockCacheKey uniquely identifies a block by (blockType, name).
// Two different block types may reuse the same name; this prevents collision.
type blockCacheKey struct {
	blockType string
	name      string
}

// blockCache holds the in-memory index built from block data.
// All query methods read from here instead of hitting SQLite.
type blockCache struct {
	groups       []BlockGroupRecord                    // all groups sorted by name
	byType       map[string][]BlockGroupRecord         // blockType -> groups
	membersByBlk map[blockCacheKey][]string            // (type,name) -> []code
	blocksByCode map[string][]BlockGroupRecord         // code -> groups
	loaded       bool
}

type BlockService struct {
	store    *Store
	provider Provider
	cfg      BlockConfig
	mu       sync.RWMutex
	cache    blockCache
}

type BlockGroupRecord struct {
	ID         int64     `xorm:"pk autoincr"`
	Name       string    `xorm:"varchar(64) index notnull"`
	BlockType  string    `xorm:"varchar(32) index notnull"`
	Source     string    `xorm:"varchar(64) index notnull"`
	StockCount int       `xorm:"notnull"`
	UpdatedAt  time.Time `xorm:"notnull"`
}

func (*BlockGroupRecord) TableName() string { return "block_group" }

type BlockMemberRecord struct {
	ID        int64     `xorm:"pk autoincr"`
	BlockName string    `xorm:"varchar(64) index notnull"`
	BlockType string    `xorm:"varchar(32) index notnull"`
	Code      string    `xorm:"varchar(16) index notnull"`
	UpdatedAt time.Time `xorm:"notnull"`
}

func (*BlockMemberRecord) TableName() string { return "block_member" }

func NewBlockService(store *Store, provider Provider, cfg BlockConfig) (*BlockService, error) {
	if store == nil {
		return nil, errors.New("block service requires collector store")
	}
	if provider == nil {
		return nil, errors.New("block service requires provider")
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = filepath.Join(DefaultBaseDir, "block")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	svc := &BlockService{
		store:    store,
		provider: provider,
		cfg:      cfg,
	}
	if err := svc.loadCacheFromDB(); err != nil {
		log.Printf("block service: cold start, DB not ready yet: %v", err)
	}
	return svc, nil
}

// SyncBlocks downloads all block files from TDX, persists to SQLite,
// then rebuilds the in-memory cache.
// On partial download failure only the successfully-fetched sources are
// replaced; data from failed sources is preserved in both DB and cache.
func (s *BlockService) SyncBlocks(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	engine, err := s.openBlockEngine()
	if err != nil {
		return err
	}
	defer engine.Close()

	if err := engine.Sync2(new(BlockGroupRecord), new(BlockMemberRecord)); err != nil {
		return err
	}

	now := s.cfg.Now()
	var allGroups []BlockGroupRecord
	var allMembers []BlockMemberRecord
	var failedCount int

	for _, filename := range blockFiles {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		infos, err := s.provider.BlockGroups(ctx, filename)
		if err != nil {
			failedCount++
			log.Printf("block sync: failed to download %s: %v (preserving old data for this source)", filename, err)
			continue
		}

		for _, info := range infos {
			allGroups = append(allGroups, BlockGroupRecord{
				Name:       info.Name,
				BlockType:  string(info.BlockType),
				Source:     info.Source,
				StockCount: len(info.Codes),
				UpdatedAt:  now,
			})
			for _, code := range info.Codes {
				allMembers = append(allMembers, BlockMemberRecord{
					BlockName: info.Name,
					BlockType: string(info.BlockType),
					Code:      code,
					UpdatedAt: now,
				})
			}
		}

		log.Printf("block sync: %s -> %d groups, %d member entries",
			filename, len(infos), countMembersForFile(infos))
	}

	if failedCount == len(blockFiles) {
		return fmt.Errorf("block sync: all %d file downloads failed, preserving existing data", failedCount)
	}

	if _, err := engine.Transaction(func(session *xorm.Session) (interface{}, error) {
		if failedCount == 0 {
			if _, err := session.Exec("DELETE FROM block_group"); err != nil {
				return nil, err
			}
			if _, err := session.Exec("DELETE FROM block_member"); err != nil {
				return nil, err
			}
		} else {
			if err := blockSelectiveDelete(session, allGroups); err != nil {
				return nil, err
			}
		}
		if err := batchInsert(session, allGroups); err != nil {
			return nil, err
		}
		if err := batchInsert(session, allMembers); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if failedCount == 0 {
		_ = s.store.UpsertCollectCursor(&CollectCursorRecord{
			Domain:     "block",
			AssetType:  MetadataAssetType,
			Instrument: "all",
			Cursor:     now.Format("20060102150405"),
		})
		s.rebuildCache(allGroups, allMembers)
	} else {
		// Partial sync: DB has a mix of preserved + new data → reload full dataset.
		var dbGroups []BlockGroupRecord
		var dbMembers []BlockMemberRecord
		if err := engine.OrderBy("name").Find(&dbGroups); err != nil {
			return fmt.Errorf("block sync: partial success but cache reload failed: %w", err)
		}
		if err := engine.Find(&dbMembers); err != nil {
			return fmt.Errorf("block sync: partial success but cache reload failed: %w", err)
		}
		s.rebuildCache(dbGroups, dbMembers)
	}

	log.Printf("block sync: completed — %d groups, %d members (cache refreshed, %d/%d files failed)",
		len(allGroups), len(allMembers), failedCount, len(blockFiles))
	return nil
}

// blockSelectiveDelete removes only the DB rows that will be replaced by
// successfully downloaded data, preserving rows from failed sources.
func blockSelectiveDelete(session *xorm.Session, newGroups []BlockGroupRecord) error {
	successSources := make(map[string]struct{})
	for _, g := range newGroups {
		successSources[g.Source] = struct{}{}
	}
	if len(successSources) == 0 {
		return nil
	}

	srcPh := make([]string, 0, len(successSources))
	srcExec := make([]interface{}, 0, len(successSources)+1)
	srcExec = append(srcExec, "") // placeholder for the SQL string
	for src := range successSources {
		srcPh = append(srcPh, "?")
		srcExec = append(srcExec, src)
	}
	srcExec[0] = "DELETE FROM block_group WHERE source IN (" + strings.Join(srcPh, ",") + ")"
	if _, err := session.Exec(srcExec...); err != nil {
		return err
	}

	namesByType := make(map[string][]string)
	for _, g := range newGroups {
		namesByType[g.BlockType] = append(namesByType[g.BlockType], g.Name)
	}
	for bt, names := range namesByType {
		ph := make([]string, len(names))
		exec := make([]interface{}, 0, len(names)+2)
		exec = append(exec, "") // placeholder for the SQL string
		exec = append(exec, bt)
		for i, n := range names {
			ph[i] = "?"
			exec = append(exec, n)
		}
		exec[0] = "DELETE FROM block_member WHERE block_type = ? AND block_name IN (" + strings.Join(ph, ",") + ")"
		if _, err := session.Exec(exec...); err != nil {
			return err
		}
	}
	return nil
}

// --------------- query methods (read from memory) ---------------

func (s *BlockService) GetBlocks(blockType BlockType) []BlockGroupRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if blockType == "" {
		out := make([]BlockGroupRecord, len(s.cache.groups))
		copy(out, s.cache.groups)
		return out
	}
	src := s.cache.byType[string(blockType)]
	out := make([]BlockGroupRecord, len(src))
	copy(out, src)
	return out
}

func (s *BlockService) GetBlockMembers(blockType, blockName string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if blockType != "" {
		src := s.cache.membersByBlk[blockCacheKey{blockType, blockName}]
		out := make([]string, len(src))
		copy(out, src)
		return out
	}
	var out []string
	for k, codes := range s.cache.membersByBlk {
		if k.name == blockName {
			out = append(out, codes...)
		}
	}
	return out
}

func (s *BlockService) GetStockBlocks(code string) []BlockGroupRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.cache.blocksByCode[code]
	out := make([]BlockGroupRecord, len(src))
	copy(out, src)
	return out
}

func (s *BlockService) SearchBlocks(keyword string) []BlockGroupRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	kw := strings.ToLower(keyword)
	var out []BlockGroupRecord
	for i := range s.cache.groups {
		if strings.Contains(strings.ToLower(s.cache.groups[i].Name), kw) {
			out = append(out, s.cache.groups[i])
		}
	}
	return out
}

func (s *BlockService) Stats() (groups, members int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, codes := range s.cache.membersByBlk {
		total += len(codes)
	}
	return len(s.cache.groups), total
}

func (s *BlockService) GetBlocksByType() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.cache.byType))
	for k, v := range s.cache.byType {
		out[k] = len(v)
	}
	return out
}

func (s *BlockService) Loaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache.loaded
}

// --------------- internal ---------------

// loadCacheFromDB loads persisted block data into memory on startup.
func (s *BlockService) loadCacheFromDB() error {
	engine, err := s.openBlockEngine()
	if err != nil {
		return err
	}
	defer engine.Close()

	if err := engine.Sync2(new(BlockGroupRecord), new(BlockMemberRecord)); err != nil {
		return err
	}

	var groups []BlockGroupRecord
	if err := engine.OrderBy("name").Find(&groups); err != nil {
		return err
	}
	var members []BlockMemberRecord
	if err := engine.Find(&members); err != nil {
		return err
	}

	if len(groups) == 0 {
		return nil
	}

	s.rebuildCache(groups, members)
	log.Printf("block service: loaded %d groups, %d members from DB into cache", len(groups), len(members))
	return nil
}

// rebuildCache replaces the in-memory index atomically (caller must hold s.mu).
func (s *BlockService) rebuildCache(groups []BlockGroupRecord, members []BlockMemberRecord) {
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

	byType := make(map[string][]BlockGroupRecord, 4)
	groupByKey := make(map[blockCacheKey]*BlockGroupRecord, len(groups))
	for i := range groups {
		g := &groups[i]
		byType[g.BlockType] = append(byType[g.BlockType], *g)
		groupByKey[blockCacheKey{g.BlockType, g.Name}] = g
	}

	membersByBlk := make(map[blockCacheKey][]string, len(groups))
	blocksByCode := make(map[string][]BlockGroupRecord, 4096)

	for i := range members {
		m := &members[i]
		key := blockCacheKey{m.BlockType, m.BlockName}
		membersByBlk[key] = append(membersByBlk[key], m.Code)
		if g, ok := groupByKey[key]; ok {
			blocksByCode[m.Code] = append(blocksByCode[m.Code], *g)
		}
	}

	for code, gs := range blocksByCode {
		seen := make(map[blockCacheKey]struct{}, len(gs))
		deduped := gs[:0]
		for _, g := range gs {
			k := blockCacheKey{g.BlockType, g.Name}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			deduped = append(deduped, g)
		}
		sort.Slice(deduped, func(i, j int) bool {
			if deduped[i].BlockType != deduped[j].BlockType {
				return deduped[i].BlockType < deduped[j].BlockType
			}
			return deduped[i].Name < deduped[j].Name
		})
		blocksByCode[code] = deduped
	}

	s.cache = blockCache{
		groups:       groups,
		byType:       byType,
		membersByBlk: membersByBlk,
		blocksByCode: blocksByCode,
		loaded:       true,
	}
}

func (s *BlockService) openBlockEngine() (*xorm.Engine, error) {
	if err := os.MkdirAll(s.cfg.BaseDir, 0o777); err != nil {
		return nil, err
	}
	return openMetadataEngine(filepath.Join(s.cfg.BaseDir, "blocks.db"))
}

func countMembersForFile(infos []BlockInfo) int {
	total := 0
	for _, info := range infos {
		total += len(info.Codes)
	}
	return total
}

func batchInsert[T any](session *xorm.Session, rows []T) error {
	const batchSize = 500
	for i := 0; i < len(rows); i += batchSize {
		end := i + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		batch := make([]interface{}, end-i)
		for j := range batch {
			batch[j] = &rows[i+j]
		}
		if _, err := session.Insert(batch...); err != nil {
			return err
		}
	}
	return nil
}
