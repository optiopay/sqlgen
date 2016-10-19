package encoding

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

func AppendArrayQuotedBytes(b, v []byte) []byte {
	b = append(b, '"')
	for {
		i := bytes.IndexAny(v, `"\`)
		if i < 0 {
			b = append(b, v...)
			break
		}
		if i > 0 {
			b = append(b, v[:i]...)
		}
		b = append(b, '\\', v[i])
		v = v[i+1:]
	}
	return append(b, '"')
}

func SplitBytes(src []byte) ([][]byte, error) {
	if len(src) < 1 || src[0] != '(' {
		return nil, fmt.Errorf("unable to parse type, expected %q at offset %d", '(', 0)
	}

	r := &scanner{bytes.NewReader(src[1:])}
	chunks := make([][]byte, 0)
	var (
		elem   []byte
		prev   byte
		depth  = 1
		quoted bool
	)
	for {
		if depth == 0 {
			// This might mean we ignore some trailing bytes
			// I don't know what this means currently
			break
		}
		b, err := r.Next()
		if err != nil && err != io.EOF {
			return nil, err
		}
		switch b {
		case ')':
			if !quoted {
				depth--
				chunks = append(chunks, elem)
			} else {
				elem = append(elem, b)
			}
		case '"':
			by, err := r.Peek()
			if err == nil {
				if by == '"' {
					elem = append(elem, b)
					_, _ = r.Next()
					break
				}
			}
			if prev != '\\' {
				quoted = !quoted
			} else {
				elem = append(elem, b)
			}
		case ',':
			if !quoted {
				chunks = append(chunks, elem)
				elem = make([]byte, 0)
			} else {
				elem = append(elem, b)
			}
		default:
			elem = append(elem, b)
		}
		prev = b
	}

	return chunks, nil
}

type scanner struct {
	*bytes.Reader
}

func (s *scanner) Next() (byte, error) {
	return s.ReadByte()
}

func (s *scanner) Peek() (byte, error) {
	b, err := s.ReadByte()
	_, err = s.Seek(-1, 1)
	if err != nil {
		panic(err)
	}
	return b, err
}

func ScanLinearArray(src, del []byte, typ string) (elems [][]byte, err error) {
	dims, elems, err := parseArray(src, del)
	if err != nil {
		return nil, err
	}
	if len(dims) > 1 {
		return nil, fmt.Errorf("pq: cannot convert ARRAY%s to %s", strings.Replace(fmt.Sprint(dims), " ", "][", -1), typ)
	}
	return elems, err
}

// parseArray extracts the dimensions and elements of an array represented in
// text format. Only representations emitted by the backend are supported.
// Notably, whitespace around brackets and delimiters is significant, and NULL
// is case-sensitive.
//
// See http://www.postgresql.org/docs/current/static/arrays.html#ARRAYS-IO
func parseArray(src, del []byte) (dims []int, elems [][]byte, err error) {
	var depth, i int

	if len(src) < 1 || src[0] != '{' {
		return nil, nil, fmt.Errorf("pq: unable to parse array; expected %q at offset %d", '{', 0)
	}

Open:
	for i < len(src) {
		switch src[i] {
		case '{':
			depth++
			i++
		case '}':
			elems = make([][]byte, 0)
			goto Close
		default:
			break Open
		}
	}
	dims = make([]int, i)

Element:
	for i < len(src) {
		switch src[i] {
		case '{':
			depth++
			dims[depth-1] = 0
			i++
		case '"':
			var elem = []byte{}
			var escape bool
			for i++; i < len(src); i++ {
				if escape {
					elem = append(elem, src[i])
					escape = false
				} else {
					switch src[i] {
					default:
						elem = append(elem, src[i])
					case '\\':
						escape = true
					case '"':
						elems = append(elems, elem)
						i++
						break Element
					}
				}
			}
		default:
			for start := i; i < len(src); i++ {
				if bytes.HasPrefix(src[i:], del) || src[i] == '}' {
					elem := src[start:i]
					if len(elem) == 0 {
						return nil, nil, fmt.Errorf("pq: unable to parse array; unexpected %q at offset %d", src[i], i)
					}
					if bytes.Equal(elem, []byte("NULL")) {
						elem = nil
					}
					elems = append(elems, elem)
					break Element
				}
			}
		}
	}

	for i < len(src) {
		if bytes.HasPrefix(src[i:], del) {
			dims[depth-1]++
			i += len(del)
			goto Element
		} else if src[i] == '}' {
			dims[depth-1]++
			depth--
			i++
		} else {
			return nil, nil, fmt.Errorf("pq: unable to parse array; unexpected %q at offset %d", src[i], i)
		}
	}

Close:
	for i < len(src) {
		if src[i] == '}' && depth > 0 {
			depth--
			i++
		} else {
			return nil, nil, fmt.Errorf("pq: unable to parse array; unexpected %q at offset %d", src[i], i)
		}
	}
	if depth > 0 {
		err = fmt.Errorf("pq: unable to parse array; expected %q at offset %d", '}', i)
	}
	if err == nil {
		for _, d := range dims {
			if (len(elems) % d) != 0 {
				err = fmt.Errorf("pq: multidimensional arrays must have elements with matching dimensions")
			}
		}
	}
	return
}