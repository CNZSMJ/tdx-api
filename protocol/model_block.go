package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	blockFileChunkSize = 0x7530 // 30000 bytes per download chunk
	blockHeaderSize    = 384
	blockSlotSize      = 2800 // fixed slot per block for stock codes
	blockNameSize      = 9
	blockCodeSize      = 7
)

// BlockFileMetaResp is the response for a block file meta request.
type BlockFileMetaResp struct {
	Size      uint32
	HashValue [32]byte
}

type blockFileMeta struct{}

func (blockFileMeta) Frame(filename string) (*Frame, error) {
	if filename == "" {
		return nil, errors.New("block file meta requires filename")
	}
	nameBytes := make([]byte, 40)
	copy(nameBytes, []byte(filename))
	return &Frame{
		Control: Control00,
		Type:    TypeBlockFileMeta,
		Data:    nameBytes,
	}, nil
}

func (blockFileMeta) Decode(bs []byte) (*BlockFileMetaResp, error) {
	if len(bs) < 37 {
		return nil, errors.New("板块文件元信息数据长度不足")
	}
	resp := &BlockFileMetaResp{
		Size: binary.LittleEndian.Uint32(bs[:4]),
	}
	copy(resp.HashValue[:], bs[5:37])
	return resp, nil
}

// BlockFileChunkResp is the response for a block file data request.
type BlockFileChunkResp struct {
	Size uint32
	Data []byte
}

type blockFileData struct{}

func (blockFileData) Frame(filename string, start, size uint32) (*Frame, error) {
	if filename == "" {
		return nil, errors.New("block file data requires filename")
	}
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, start)
	binary.Write(buf, binary.LittleEndian, size)
	nameBytes := make([]byte, 300)
	copy(nameBytes, []byte(filename))
	buf.Write(nameBytes)
	return &Frame{
		Control: Control00,
		Type:    TypeBlockFileData,
		Data:    buf.Bytes(),
	}, nil
}

func (blockFileData) Decode(bs []byte) (*BlockFileChunkResp, error) {
	if len(bs) < 4 {
		return nil, errors.New("板块文件数据长度不足")
	}
	size := binary.LittleEndian.Uint32(bs[:4])
	data := bs[4:]
	if uint32(len(data)) < size {
		return nil, fmt.Errorf("板块文件数据长度不匹配,预期%d,得到%d", size, len(data))
	}
	return &BlockFileChunkResp{
		Size: size,
		Data: data[:size],
	}, nil
}

// BlockGroup represents a parsed block (sector) group from a TDX block file.
type BlockGroup struct {
	Name       string
	BlockType  uint16
	StockCount uint16
	Codes      []string
}

// ParseBlockFile parses a complete TDX block binary file (e.g. block.dat)
// into a list of BlockGroup entries.
func ParseBlockFile(data []byte) ([]BlockGroup, error) {
	minSize := blockHeaderSize + 2
	if len(data) < minSize {
		return nil, fmt.Errorf("block file too small: %d bytes (need >= %d)", len(data), minSize)
	}

	pos := blockHeaderSize
	total := binary.LittleEndian.Uint16(data[pos : pos+2])
	pos += 2

	groups := make([]BlockGroup, 0, total)
	for i := uint16(0); i < total; i++ {
		if pos+blockNameSize+4 > len(data) {
			return groups, fmt.Errorf("truncated at block %d/%d", i, total)
		}

		nameRaw := data[pos : pos+blockNameSize]
		name := gbkCString(nameRaw)
		pos += blockNameSize

		stockCount := binary.LittleEndian.Uint16(data[pos : pos+2])
		pos += 2

		blockType := binary.LittleEndian.Uint16(data[pos : pos+2])
		pos += 2

		blockStockBegin := pos
		codes := make([]string, 0, stockCount)
		for j := uint16(0); j < stockCount; j++ {
			if pos+blockCodeSize > len(data) {
				break
			}
			codeRaw := data[pos : pos+blockCodeSize]
			code := gbkCString(codeRaw)
			if code != "" {
				codes = append(codes, code)
			}
			pos += blockCodeSize
		}

		pos = blockStockBegin + blockSlotSize

		groups = append(groups, BlockGroup{
			Name:       name,
			BlockType:  blockType,
			StockCount: stockCount,
			Codes:      codes,
		})
	}

	return groups, nil
}
