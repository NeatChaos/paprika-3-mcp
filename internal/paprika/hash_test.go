package paprika

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

func TestComputeHashFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/recipe_hashes.json")
	require.NoError(t, err)

	var fixtures []struct {
		Name         string `json:"name"`
		Recipe       Recipe `json:"recipe"`
		ExpectedHash string `json:"expected_hash"`
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.NotEmpty(t, fixtures)

	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got, err := f.Recipe.ComputeHash()
			require.NoError(t, err)
			assert.Equal(t, f.ExpectedHash, got)
		})
	}

	t.Run("hash sent by the macOS app when creating a recipe", func(t *testing.T) {
		var r Recipe
		require.NoError(t, json.Unmarshal(readWireFixture(t), &r))
		got, err := r.ComputeHash()
		require.NoError(t, err)
		assert.Equal(t, r.Hash, got)
	})
}

func TestComputeHashInvariants(t *testing.T) {
	base := Recipe{
		UID:        "INVARIANT-UID",
		Name:       "Soup",
		Created:    "2024-01-02 03:04:05",
		Categories: []string{"B-CAT", "A-CAT"},
		Scale:      nil,
	}
	baseHash, err := base.ComputeHash()
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^[0-9A-F]{64}$`), baseHash)

	same := map[string]func(r *Recipe){
		"hash":             func(r *Recipe) { r.Hash = "ANYTHING" },
		"category order":   func(r *Recipe) { r.Categories = []string{"A-CAT", "B-CAT"} },
		"in_trash":         func(r *Recipe) { r.InTrash = true },
		"deleted":          func(r *Recipe) { r.Deleted = true },
		"trash and delete": func(r *Recipe) { r.InTrash, r.Deleted = true, true },
	}
	for name, mutate := range same {
		t.Run("ignores "+name, func(t *testing.T) {
			r := base
			r.Categories = append([]string(nil), base.Categories...)
			mutate(&r)
			got, err := r.ComputeHash()
			require.NoError(t, err)
			assert.Equal(t, baseHash, got)
		})
	}

	t.Run("nil and empty scale differ", func(t *testing.T) {
		r := base
		r.Scale = strPtr("")
		got, err := r.ComputeHash()
		require.NoError(t, err)
		assert.NotEqual(t, baseHash, got)
	})

	t.Run("nil categories hash like empty categories", func(t *testing.T) {
		a, b := base, base
		a.Categories, b.Categories = nil, []string{}
		ha, err := a.ComputeHash()
		require.NoError(t, err)
		hb, err := b.ComputeHash()
		require.NoError(t, err)
		assert.Equal(t, hb, ha)
	})
}

func TestHashSerialization(t *testing.T) {
	r := Recipe{Name: "a<b>&c/d" + string(rune(0x2028)) + "e😀\bf\tg"}
	got, err := hashInput(r)
	require.NoError(t, err)

	want := []byte(`"a<b>&c\/d`)
	want = append(want, 0xE2, 0x80, 0xA8)
	want = append(want, []byte(`e😀\bf\tg"`)...)
	assert.True(t, bytes.Contains(got, want), "hash input %q does not contain %q", got, want)
	assert.False(t, bytes.HasSuffix(got, []byte("\n")))
}

func TestRecipeWireRoundTrip(t *testing.T) {
	wire := readWireFixture(t)

	var r Recipe
	require.NoError(t, json.Unmarshal(wire, &r))
	out, err := json.Marshal(r)
	require.NoError(t, err)

	var want, got map[string]any
	require.NoError(t, json.Unmarshal(wire, &want))
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, want, got)
	assert.Contains(t, got, "scale")
	assert.Nil(t, got["scale"])
}

func readWireFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/recipe_wire.json")
	require.NoError(t, err)
	return data
}
