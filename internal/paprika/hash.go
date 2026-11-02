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
	// encoding/json always escapes these two line separators; NSJSONSerialization does not.
	for _, sep := range []rune{0x2028, 0x2029} {
		out = bytes.ReplaceAll(out, fmt.Appendf(nil, `\u%04x`, sep), []byte(string(sep)))
	}
	return out, nil
}
