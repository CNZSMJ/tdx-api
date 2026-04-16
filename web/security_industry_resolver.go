package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	tdxInconPathEnv           = "TDX_INCON_PATH"
	tdxIndustryAssignmentsTTL = 30 * time.Minute
)

type securityIndustryAssignment struct {
	FullCode            string
	PrimaryIndustryCode string
	RefinedIndustryCode string
}

type securityIndustryResolver struct {
	mu sync.Mutex

	pathResolver        func() string
	readFile            func(string) ([]byte, error)
	downloadAssignments func() ([]byte, error)
	now                 func() time.Time
	assignmentsTTL      time.Duration

	cachedDictionaryPath    string
	cachedDictionaryModTime time.Time
	cachedPrimaryNames      map[string]string
	cachedRefinedNames      map[string]string

	cachedAssignments  map[string]securityIndustryAssignment
	assignmentsRefresh time.Time
}

func newSecurityIndustryResolver() *securityIndustryResolver {
	return &securityIndustryResolver{
		pathResolver: defaultSecurityIndustryDictionaryPath,
		readFile:     os.ReadFile,
		downloadAssignments: func() ([]byte, error) {
			if client == nil {
				return nil, errors.New("TDX client 未初始化")
			}
			return client.DownloadBlockFile("tdxhy.cfg")
		},
		now:            time.Now,
		assignmentsTTL: tdxIndustryAssignmentsTTL,
	}
}

func (r *securityIndustryResolver) Resolve(fullCode string) (string, string) {
	fullCode = normalizeSecurityFullCode(fullCode)
	if fullCode == "" {
		return "", ""
	}

	path := strings.TrimSpace(r.pathResolver())
	if path == "" {
		return "", ""
	}

	primaryNames, refinedNames, err := r.loadDictionary(path)
	if err != nil {
		return "", ""
	}
	assignments, err := r.loadAssignments()
	if err != nil {
		return "", ""
	}
	assignment, ok := assignments[fullCode]
	if !ok {
		return "", ""
	}

	industryName := strings.TrimSpace(primaryNames[assignment.PrimaryIndustryCode])
	subindustryName := strings.TrimSpace(refinedNames[assignment.RefinedIndustryCode])
	if industryName == "" {
		industryName = subindustryName
		subindustryName = ""
	}
	return industryName, subindustryName
}

func (r *securityIndustryResolver) loadDictionary(path string) (map[string]string, map[string]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if path == r.cachedDictionaryPath &&
		!r.cachedDictionaryModTime.IsZero() &&
		info.ModTime().Equal(r.cachedDictionaryModTime) &&
		r.cachedPrimaryNames != nil &&
		r.cachedRefinedNames != nil {
		return r.cachedPrimaryNames, r.cachedRefinedNames, nil
	}

	raw, err := r.readFile(path)
	if err != nil {
		if r.cachedPrimaryNames != nil && r.cachedRefinedNames != nil {
			return r.cachedPrimaryNames, r.cachedRefinedNames, nil
		}
		return nil, nil, err
	}

	primaryNames, refinedNames := parseSecurityIndustryDictionary(raw)
	r.cachedDictionaryPath = path
	r.cachedDictionaryModTime = info.ModTime()
	r.cachedPrimaryNames = primaryNames
	r.cachedRefinedNames = refinedNames
	return primaryNames, refinedNames, nil
}

func (r *securityIndustryResolver) loadAssignments() (map[string]securityIndustryAssignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if r.cachedAssignments != nil && now.Before(r.assignmentsRefresh) {
		return r.cachedAssignments, nil
	}

	raw, err := r.downloadAssignments()
	if err != nil {
		if r.cachedAssignments != nil {
			return r.cachedAssignments, nil
		}
		return nil, err
	}

	assignments := parseSecurityIndustryAssignments(raw)
	r.cachedAssignments = assignments
	r.assignmentsRefresh = now.Add(r.assignmentsTTL)
	return assignments, nil
}

func parseSecurityIndustryDictionary(raw []byte) (map[string]string, map[string]string) {
	primaryNames := make(map[string]string)
	refinedNames := make(map[string]string)

	decoded := raw
	if !utf8.Valid(raw) {
		utf8Raw, err := io.ReadAll(transform.NewReader(bytes.NewReader(raw), simplifiedchinese.GBK.NewDecoder()))
		if err == nil {
			decoded = utf8Raw
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(decoded))
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "######" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		code := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if code == "" || name == "" {
			continue
		}
		switch section {
		case "TDXNHY":
			primaryNames[code] = name
		case "TDXRSHY":
			refinedNames[code] = name
		}
	}

	return primaryNames, refinedNames
}

func parseSecurityIndustryAssignments(raw []byte) map[string]securityIndustryAssignment {
	assignments := make(map[string]securityIndustryAssignment)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 6 {
			continue
		}
		fullCode := buildSecurityFullCode(fields[0], fields[1])
		if fullCode == "" {
			continue
		}
		assignments[fullCode] = securityIndustryAssignment{
			FullCode:            fullCode,
			PrimaryIndustryCode: strings.TrimSpace(fields[2]),
			RefinedIndustryCode: strings.TrimSpace(fields[5]),
		}
	}
	return assignments
}

func buildSecurityFullCode(market, code string) string {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return ""
	}
	switch {
	case strings.HasPrefix(code, "8"), strings.HasPrefix(code, "43"), strings.HasPrefix(code, "92"):
		return "bj" + code
	case strings.TrimSpace(market) == "1":
		return "sh" + code
	default:
		return "sz" + code
	}
}

func normalizeSecurityFullCode(fullCode string) string {
	return strings.ToLower(strings.TrimSpace(fullCode))
}

func defaultSecurityIndustryDictionaryPath() string {
	if raw := strings.TrimSpace(os.Getenv(tdxInconPathEnv)); raw != "" {
		return raw
	}

	candidates := make([]string, 0, 4)
	if databaseDir != "" {
		candidates = append(candidates,
			filepath.Join(databaseDir, "incon.dat"),
			filepath.Join(databaseDir, "metadata", "incon.dat"),
			filepath.Join(filepath.Dir(databaseDir), "incon.dat"),
			filepath.Join(filepath.Dir(databaseDir), "T0002", "incon.dat"),
		)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
