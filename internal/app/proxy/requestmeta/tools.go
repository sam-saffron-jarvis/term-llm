package requestmeta

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strconv"
)

// Gemini's tools array contains groups, not necessarily one declaration each.
// Count only evidenced declarations and googleSearch, skipping names/schemas.
// Unknown tool variants suppress the count rather than inventing a total.
func (s *settings) geminiTools(d *jsontext.Decoder) error {
	if d.PeekKind() != '[' {
		s.bad = true
		return d.SkipValue()
	}
	if _, err := d.ReadToken(); err != nil {
		return err
	}
	var n int64
	known := true
	for d.PeekKind() != ']' {
		if d.PeekKind() != '{' {
			known = false
			if err := d.SkipValue(); err != nil {
				return err
			}
			continue
		}
		if _, err := d.ReadToken(); err != nil {
			return err
		}
		for d.PeekKind() != '}' {
			key, err := d.ReadToken()
			if err != nil {
				return err
			}
			switch key.String() {
			case "functionDeclarations":
				if d.PeekKind() != '[' {
					known = false
					err = d.SkipValue()
				} else {
					var count int64
					count, err = countObjects(d)
					if count < 0 {
						known = false
					} else {
						n += count
					}
				}
			case "googleSearch":
				if d.PeekKind() != '{' {
					known = false
				} else {
					n++
				}
				err = d.SkipValue()
			default:
				known = false
				err = d.SkipValue()
			}
			if err != nil {
				return err
			}
		}
		if _, err := d.ReadToken(); err != nil {
			return err
		}
	}
	if _, err := d.ReadToken(); err != nil {
		return err
	}
	if n > 256 {
		s.bad = true
		return nil
	}
	if known {
		s.values["declared_tools"] = json.RawMessage(strconv.FormatInt(n, 10))
	}
	return nil
}

func countObjects(d *jsontext.Decoder) (int64, error) {
	if _, err := d.ReadToken(); err != nil {
		return 0, err
	}
	var n int64
	valid := true
	for d.PeekKind() != ']' {
		if d.PeekKind() != '{' {
			valid = false
		}
		if err := d.SkipValue(); err != nil {
			return 0, err
		}
		n++
	}
	_, err := d.ReadToken()
	if !valid {
		n = -1
	}
	return n, err
}
