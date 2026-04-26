package lifecycle

import (
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const ColdURIScheme = "tdx-cold"

type ColdURI struct {
	Dataset string
	Path    string
}

func ParseColdURI(raw string) (ColdURI, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ColdURI{}, err
	}
	if parsed.Scheme != ColdURIScheme {
		return ColdURI{}, errors.New("unsupported cold uri scheme")
	}
	if parsed.Host == "" {
		return ColdURI{}, errors.New("cold uri dataset is required")
	}
	p := strings.TrimPrefix(parsed.EscapedPath(), "/")
	unescaped, err := url.PathUnescape(p)
	if err != nil {
		return ColdURI{}, err
	}
	clean := path.Clean(unescaped)
	if clean == "." || clean == "" {
		return ColdURI{}, errors.New("cold uri path is required")
	}
	if strings.HasPrefix(clean, "../") || clean == ".." || path.IsAbs(clean) {
		return ColdURI{}, errors.New("cold uri path traversal is not allowed")
	}
	if clean != unescaped {
		return ColdURI{}, errors.New("cold uri path must be normalized")
	}
	return ColdURI{Dataset: parsed.Host, Path: clean}, nil
}

func FormatColdURI(uri ColdURI) string {
	return ColdURIScheme + "://" + uri.Dataset + "/" + strings.TrimPrefix(uri.Path, "/")
}

func MustFormatColdURI(uri ColdURI) string {
	raw := FormatColdURI(uri)
	if _, err := ParseColdURI(raw); err != nil {
		panic(err)
	}
	return raw
}

func LocalPathForColdURI(root, raw string) (string, error) {
	uri, err := ParseColdURI(raw)
	if err != nil {
		return "", err
	}
	parts := append([]string{root, uri.Dataset}, strings.Split(uri.Path, "/")...)
	return filepath.Join(parts...), nil
}
