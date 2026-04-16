package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf8"
)

type CompanyInfoCategory struct {
	Name     string
	Filename string
	Start    uint32
	Length   uint32
}

type CompanyInfoCategories []*CompanyInfoCategory

type companyInfoCategory struct{}

func (companyInfoCategory) Frame(code string) (*Frame, error) {
	exchange, number, err := DecodeCode(code)
	if err != nil {
		return nil, err
	}
	data := append(Bytes(uint16(exchange)), []byte(number)...)
	data = append(data, Bytes(uint32(0))...)
	return &Frame{
		Control: Control01,
		Type:    TypeCompanyInfoCategory,
		Data:    data,
	}, nil
}

func (companyInfoCategory) Decode(bs []byte) (CompanyInfoCategories, error) {
	if len(bs) < 2 {
		return nil, errors.New("F10目录数据长度不足")
	}

	count := Uint16(bs[:2])
	bs = bs[2:]
	resp := make(CompanyInfoCategories, 0, count)

	for i := uint16(0); i < count; i++ {
		if len(bs) < 152 {
			return nil, errors.New("F10目录数据长度不足")
		}
		item := &CompanyInfoCategory{
			Name:     sanitizeCategoryName(gbkCString(bs[:64])),
			Filename: sanitizeCategoryFilename(gbkCString(bs[64:144])),
			Start:    binary.LittleEndian.Uint32(bs[144:148]),
			Length:   binary.LittleEndian.Uint32(bs[148:152]),
		}
		resp = append(resp, item)
		bs = bs[152:]
	}

	return resp, nil
}

type companyInfoContent struct{}

func (companyInfoContent) Frame(code, filename string, start, length uint32) (*Frame, error) {
	exchange, number, err := DecodeCode(code)
	if err != nil {
		return nil, err
	}

	filenameBytes := make([]byte, 80)
	copy(filenameBytes, []byte(filename))

	data := append(Bytes(uint16(exchange)), []byte(number)...)
	data = append(data, Bytes(uint16(0))...)
	data = append(data, filenameBytes...)
	data = append(data, Bytes(start)...)
	data = append(data, Bytes(length)...)
	data = append(data, Bytes(uint32(0))...)

	return &Frame{
		Control: Control01,
		Type:    TypeCompanyInfoContent,
		Data:    data,
	}, nil
}

func (companyInfoContent) Decode(bs []byte) (string, error) {
	if len(bs) < 12 {
		return "", errors.New("F10正文数据长度不足")
	}

	length := binary.LittleEndian.Uint16(bs[10:12])
	if len(bs) < 12+int(length) {
		return "", errors.New("F10正文数据长度不足")
	}

	return string(UTF8ToGBK(bs[12 : 12+length])), nil
}

func gbkCString(bs []byte) string {
	if idx := bytes.IndexByte(bs, 0); idx >= 0 {
		bs = bs[:idx]
	}
	text := string(UTF8ToGBK(bs))
	text = strings.Map(func(r rune) rune {
		switch {
		case r == utf8.RuneError:
			return -1
		case r < 32:
			return -1
		default:
			return r
		}
	}, text)
	return strings.TrimSpace(text)
}

func sanitizeCategoryName(text string) string {
	for len(text) > 0 {
		r, size := utf8.DecodeLastRuneInString(text)
		if r > 127 {
			break
		}
		text = text[:len(text)-size]
	}
	return strings.TrimSpace(text)
}

func sanitizeCategoryFilename(text string) string {
	if idx := strings.Index(strings.ToLower(text), ".txt"); idx >= 0 {
		text = text[:idx+4]
	}
	return strings.TrimSpace(text)
}
