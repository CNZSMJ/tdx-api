package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

func LogicalChecksum(rows []map[string]any, stableKeys []string, fieldOrder []string) (string, error) {
	if len(stableKeys) == 0 {
		return "", errors.New("stable keys are required")
	}
	if len(fieldOrder) == 0 {
		return "", errors.New("field order is required")
	}
	ordered := append([]map[string]any(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return rowSortKey(ordered[i], stableKeys) < rowSortKey(ordered[j], stableKeys)
	})
	h := sha256.New()
	for _, row := range ordered {
		for _, field := range fieldOrder {
			encoded, err := encodeChecksumValue(row[field])
			if err != nil {
				return "", fmt.Errorf("field %s: %w", field, err)
			}
			_, _ = h.Write([]byte(field))
			_, _ = h.Write([]byte("="))
			_, _ = h.Write([]byte(encoded))
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func FileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func rowSortKey(row map[string]any, stableKeys []string) string {
	parts := make([]string, 0, len(stableKeys))
	for _, key := range stableKeys {
		value, _ := encodeChecksumValue(row[key])
		parts = append(parts, value)
	}
	return strings.Join(parts, "\x00")
}

func encodeChecksumValue(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "<null>", nil
	case string:
		return strconv.Quote(v), nil
	case int:
		return strconv.FormatInt(int64(v), 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", errors.New("NaN/Inf floats are not allowed")
		}
		return strconv.FormatFloat(f, 'G', 16, 64), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", errors.New("NaN/Inf floats are not allowed")
		}
		return strconv.FormatFloat(v, 'G', 16, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}
