package paprika

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ComputeHash returns the content hash Paprika's apps compute for a recipe: the uppercase hex
// SHA-256 of a JSON array of 27 fields in alphabetical wire-key order, serialized the way
// NSJSONSerialization does it. Ported from bojanrajkovic/mcp-paprika (MIT).
func (r Recipe) ComputeHash() (string, error) {
	input, err := hashInput(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(input)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

// hashInput builds the serialized hash input. hash is blanked because it is self-referential;
// deleted and in_trash are pinned false because Paprika keeps a recipe's hash across trash flips.
func hashInput(r Recipe) ([]byte, error) {
	categories := slices.Clone(r.Categories)
	if categories == nil {
		categories = []string{}
	}
	slices.Sort(categories)

	values := []any{
		categories,
		r.CookTime,
		r.Created,
		false,
		r.Description,
		r.Difficulty,
		r.Directions,
		nil,
		r.ImageURL,
		false,
		r.Ingredients,
		r.IsPinned,
		r.Name,
		r.Notes,
		r.NutritionalInfo,
		r.OnFavorites,
		r.Photo,
		r.PhotoHash,
		r.PhotoLarge,
		r.PrepTime,
		r.Rating,
		r.Scale,
		r.Servings,
		r.Source,
		r.SourceURL,
		r.TotalTime,
		r.UID,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(values); err != nil {
		return nil, err
	}

	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	out = bytes.ReplaceAll(out, []byte("/"), []byte(`\/`))
	return unescapeLineSeparators(out), nil
}

// unescapeLineSeparators undoes encoding/json's escaping of U+2028 and U+2029, which NSJSONSerialization
// leaves raw. An escape only starts at a backslash preceded by an even run of backslashes.
func unescapeLineSeparators(in []byte) []byte {
	out := make([]byte, 0, len(in))
	run := 0
	for i := 0; i < len(in); i++ {
		if in[i] == '\\' && run%2 == 0 {
			if sep, ok := lineSeparatorEscape(in[i:]); ok {
				out = append(out, string(sep)...)
				i += 5
				run = 0
				continue
			}
		}
		out = append(out, in[i])
		if in[i] == '\\' {
			run++
		} else {
			run = 0
		}
	}
	return out
}

func lineSeparatorEscape(b []byte) (rune, bool) {
	for _, sep := range []rune{0x2028, 0x2029} {
		if bytes.HasPrefix(b, fmt.Appendf(nil, `\u%04x`, sep)) {
			return sep, true
		}
	}
	return 0, false
}
