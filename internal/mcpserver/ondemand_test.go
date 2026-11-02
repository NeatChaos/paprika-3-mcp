package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/soggycactus/paprika-3-mcp/internal/cachestore"
	"github.com/soggycactus/paprika-3-mcp/internal/paprika"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is an in-memory GCS object with generation preconditions
type fakeStore struct {
	mu      sync.Mutex
	data    []byte
	gen     int64
	loads   int
	saves   int
	saveErr error
}

func (s *fakeStore) Load(ctx context.Context) ([]byte, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.data == nil {
		return nil, 0, cachestore.ErrNotFound
	}
	return append([]byte(nil), s.data...), s.gen, nil
}

func (s *fakeStore) Save(ctx context.Context, data []byte, ifGeneration int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.saveErr != nil {
		return 0, s.saveErr
	}
	if ifGeneration != s.gen {
		return 0, cachestore.ErrPrecondition
	}
	s.data = append([]byte(nil), data...)
	s.gen++
	return s.gen, nil
}

func (s *fakeStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

// storeWith returns a store holding the client's current non-trashed recipes, categories and meal types
func storeWith(t *testing.T, client *fakeClient) *fakeStore {
	t.Helper()
	obj := storeObject{Version: storeVersion, Recipes: map[string]paprika.Recipe{}, Hashes: map[string]string{}, Categories: client.categories, MealTypes: client.mealTypes}
	for uid, r := range client.recipes {
		obj.Hashes[uid] = r.Hash
		if !r.InTrash {
			obj.Recipes[uid] = r
		}
	}
	data, err := json.Marshal(obj)
	require.NoError(t, err)
	return &fakeStore{data: data, gen: 1}
}

func newOnDemandServer(client *fakeClient, store cachestore.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := newServer(client, "test", logger, "")
	s.cache.enableOnDemand(store)
	return s
}

func (f *fakeClient) takeCalls() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	f.getCalls = nil
	return calls
}

func twoRecipeAccount() *fakeClient {
	client := newFakeAccount()
	for _, uid := range []string{"R3", "R4", "R5"} {
		delete(client.recipes, uid)
	}
	return client
}

// warmUp triggers the cold-start load and forgets the requests it made
func warmUp(t *testing.T, s *Server, client *fakeClient) {
	t.Helper()
	_, isErr := callTool(t, s.listCategories, nil)
	require.False(t, isErr)
	client.takeCalls()
}

func TestColdStartLoadsOnce(t *testing.T) {
	client := twoRecipeAccount()
	store := storeWith(t, client)
	s := newOnDemandServer(client, store, nil)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var req mcp.CallToolRequest
			req.Params.Arguments = map[string]interface{}{"query": "prei"}
			result, err := s.searchRecipes(context.Background(), req)
			if assert.NoError(t, err) {
				assert.False(t, result.IsError)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, map[string]int{"ListCategories": 1, "ListMealTypes": 1, "ListMeals": 1, "ListRecipes": 1}, client.takeCalls())
	assert.Equal(t, 1, store.loads)

	for range 10 {
		out, _ := callTool(t, s.searchRecipes, map[string]interface{}{"query": "prei"})
		assert.Contains(t, out, "Found 2 recipes")
		out, _ = callTool(t, s.getRecipe, map[string]interface{}{"recipe": "R1"})
		assert.Contains(t, out, "# Pasta met prei")
		callTool(t, s.listCategories, nil)
		out, _ = callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
		assert.Contains(t, out, "Yoghurt (free text, no recipe, meal uid: M2)")
	}
	assert.Empty(t, client.takeCalls())
	assert.Zero(t, store.saveCount(), "an unchanged cache is not written back")
}

func TestColdStartRetriesAfterFailure(t *testing.T) {
	client := newFakeAccount()
	client.failList = true
	s := newOnDemandServer(client, &fakeStore{}, nil)

	_, err := s.searchRecipes(context.Background(), mcp.CallToolRequest{})
	assert.Error(t, err)

	client.failList = false
	out, isErr := callTool(t, s.searchRecipes, map[string]interface{}{"query": "prei"})
	assert.False(t, isErr)
	assert.Contains(t, out, "Found 2 recipes")
}

func TestColdStartOutlivesCaller(t *testing.T) {
	client := newFakeAccount()
	client.gate = make(chan struct{})
	s := newOnDemandServer(client, &fakeStore{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := s.searchRecipes(ctx, mcp.CallToolRequest{})
		done <- err
	}()
	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)

	close(client.gate)
	out, isErr := callTool(t, s.searchRecipes, map[string]interface{}{"query": "prei"})
	assert.False(t, isErr)
	assert.Contains(t, out, "Found 2 recipes")
	assert.Equal(t, 1, client.takeCalls()["ListRecipes"], "the load started by the cancelled call is reused")
}

func TestRefreshTool(t *testing.T) {
	client := newFakeAccount()
	store := storeWith(t, client)
	s := newOnDemandServer(client, store, nil)
	warmUp(t, s, client)

	r := client.recipes["R2"]
	r.Hash = "h2-changed"
	r.Name = "Preisoep deluxe"
	client.recipes["R2"] = r

	out, isErr := callTool(t, s.refreshPaprika, nil)
	assert.False(t, isErr)
	assert.Contains(t, out, "4 recipes (1 added or changed)")
	assert.Contains(t, out, "5 categories")
	assert.Contains(t, out, "4 meal types")
	assert.Contains(t, out, "4 planned meals")
	assert.Equal(t, []string{"R2"}, client.getCalls)
	assert.Equal(t, 1, store.saveCount())

	client.takeCalls()
	out, _ = callTool(t, s.getRecipe, map[string]interface{}{"recipe": "R2"})
	assert.Contains(t, out, "# Preisoep deluxe")
	assert.Empty(t, client.takeCalls())
}

func TestUpdateRecipeConflict(t *testing.T) {
	for name, edit := range map[string]func(*paprika.Recipe){
		"stored hash changed":           func(r *paprika.Recipe) { r.Hash = "H2"; r.Name = "Pasta met prei v2" },
		"same stored hash, new content": func(r *paprika.Recipe) { r.Name = "Pasta met prei v2" },
	} {
		t.Run(name, func(t *testing.T) {
			client := newFakeAccount()
			r := client.recipes["R1"]
			r.Hash = "H1"
			client.recipes["R1"] = r
			s := newOnDemandServer(client, storeWith(t, client), nil)
			warmUp(t, s, client)

			edit(&r)
			client.recipes["R1"] = r

			out, isErr := callTool(t, s.updateRecipe, map[string]interface{}{"uid": "R1", "notes": "Lekker"})
			assert.True(t, isErr)
			assert.Equal(t, conflictMessage, out)
			assert.Empty(t, client.savedRecipe)

			out, _ = callTool(t, s.getRecipe, map[string]interface{}{"recipe": "R1"})
			assert.Contains(t, out, "# Pasta met prei v2")
			assert.Equal(t, map[string]int{"GetRecipe": 1}, client.takeCalls())
		})
	}
}

func TestUpdateRecipeNoConflict(t *testing.T) {
	client := newFakeAccount()
	store := storeWith(t, client)
	s := newOnDemandServer(client, store, nil)
	warmUp(t, s, client)

	out, isErr := callTool(t, s.updateRecipe, map[string]interface{}{"uid": "R1", "notes": "Lekker"})
	assert.False(t, isErr, out)
	assert.Equal(t, map[string]int{"GetRecipe": 1, "SaveRecipe": 1}, client.takeCalls())
	assert.Equal(t, 1, store.saveCount())
	assert.Contains(t, string(store.data), "Lekker")

	out, _ = callTool(t, s.getRecipe, map[string]interface{}{"recipe": "R1"})
	assert.Contains(t, out, "Lekker")
}

func TestRemoveMealConflict(t *testing.T) {
	for name, edit := range map[string]func(*fakeClient){
		"changed in the app": func(c *fakeClient) { c.meals[1].Name = "Muesli" },
		"removed in the app": func(c *fakeClient) { c.meals = append(c.meals[:1], c.meals[2:]...) },
	} {
		t.Run(name, func(t *testing.T) {
			client := newFakeAccount()
			s := newOnDemandServer(client, &fakeStore{}, nil)
			warmUp(t, s, client)

			edit(client)
			_, isErr := callTool(t, s.removeMeal, map[string]interface{}{"uid": "M2"})
			assert.True(t, isErr)
			assert.Empty(t, client.savedMeals)
			assert.Equal(t, map[string]int{"ListMeals": 1}, client.takeCalls())

			out, _ := callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
			assert.NotContains(t, out, "Yoghurt", "the meal plan was reloaded")
			assert.Empty(t, client.takeCalls())
		})
	}
}

func TestRemoveMealOnDemand(t *testing.T) {
	client := newFakeAccount()
	s := newOnDemandServer(client, &fakeStore{}, nil)
	warmUp(t, s, client)

	_, isErr := callTool(t, s.removeMeal, map[string]interface{}{"uid": "M2"})
	assert.False(t, isErr)
	require.Len(t, client.savedMeals, 1)
	assert.True(t, client.savedMeals[0].Deleted)
	assert.Equal(t, map[string]int{"ListMeals": 1, "SaveMeal": 1}, client.takeCalls())

	out, _ := callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
	assert.NotContains(t, out, "Yoghurt")

	_, isErr = callTool(t, s.removeMeal, map[string]interface{}{"uid": "NOPE"})
	assert.True(t, isErr)
}

func TestAddMealOnDemand(t *testing.T) {
	client := newFakeAccount()
	s := newOnDemandServer(client, &fakeStore{}, nil)
	warmUp(t, s, client)

	_, isErr := callTool(t, s.addMeal, map[string]interface{}{"date": "2025-03-10", "meal_type": "lunch", "name": "Tosti"})
	assert.False(t, isErr)
	assert.Equal(t, map[string]int{"SaveMeal": 1}, client.takeCalls())

	out, _ := callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
	assert.Contains(t, out, "Lunch: Tosti")
	assert.Empty(t, client.takeCalls())
}

func TestCreateRecipeUnknownCategory(t *testing.T) {
	client := newFakeAccount()
	s := newOnDemandServer(client, &fakeStore{}, nil)
	warmUp(t, s, client)

	out, isErr := callTool(t, s.createRecipe, map[string]interface{}{"name": "Ravioli", "ingredients": "pasta", "directions": "koken", "categories": []interface{}{"Italiaans"}})
	assert.True(t, isErr)
	assert.Contains(t, out, `"Italiaans"`)
	assert.Contains(t, out, "refresh_paprika")
	assert.Empty(t, client.takeCalls())
}

func TestCreateRecipeOnDemandSavesStore(t *testing.T) {
	client := newFakeAccount()
	store := storeWith(t, client)
	s := newOnDemandServer(client, store, nil)
	warmUp(t, s, client)

	_, isErr := callTool(t, s.createRecipe, map[string]interface{}{"name": "Ravioli", "ingredients": "pasta", "directions": "koken"})
	assert.False(t, isErr)
	assert.Equal(t, map[string]int{"SaveRecipe": 1}, client.takeCalls())
	assert.Equal(t, 1, store.saveCount())
	assert.Contains(t, string(store.data), "Ravioli")
}

func TestCacheCorruptOrWrongVersion(t *testing.T) {
	for _, data := range []string{`{nope`, `{"version":2,"recipes":{},"hashes":{},"categories":[],"meal_types":[]}`} {
		t.Run(data, func(t *testing.T) {
			client := newFakeAccount()
			store := &fakeStore{data: []byte(data), gen: 3}
			s := newOnDemandServer(client, store, nil)

			out, isErr := callTool(t, s.searchRecipes, map[string]interface{}{"query": "prei"})
			assert.False(t, isErr)
			assert.Contains(t, out, "Found 2 recipes")
			assert.Len(t, client.getCalls, 5)
			assert.Equal(t, int64(4), store.gen, "the bad object is replaced under a generation precondition")
			assert.Contains(t, string(store.data), `"version":1`)
		})
	}
}

func TestCachePreconditionSkipsSave(t *testing.T) {
	var logs bytes.Buffer
	client := newFakeAccount()
	store := storeWith(t, client)
	store.saveErr = cachestore.ErrPrecondition
	s := newOnDemandServer(client, store, slog.New(slog.NewTextHandler(&logs, nil)))
	warmUp(t, s, client)

	out, isErr := callTool(t, s.updateRecipe, map[string]interface{}{"uid": "R1", "notes": "Lekker"})
	assert.False(t, isErr, out)
	assert.Equal(t, 1, store.saveCount(), "no retry")
	assert.NotContains(t, string(store.data), "Lekker", "no overwrite")
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "precondition")
}

func TestUpstreamModeUnchanged(t *testing.T) {
	client := newFakeAccount()
	s := newTestServer(client)

	for _, tool := range s.tools() {
		assert.NotEqual(t, "refresh_paprika", tool.Tool.Name)
	}
	callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
	callTool(t, s.listMeals, map[string]interface{}{"start_date": "2025-03-10"})
	assert.Equal(t, 2, client.takeCalls()["ListMeals"], "upstream reads meals live")

	assert.Error(t, validateOptions(NewServerOptions{Store: &fakeStore{}}), "a store without on-demand refresh is rejected")
	assert.NoError(t, validateOptions(NewServerOptions{Store: &fakeStore{}, OnDemandRefresh: true}))
}

func TestOnDemandToolsIncludeRefresh(t *testing.T) {
	s := newOnDemandServer(newFakeAccount(), &fakeStore{}, nil)
	var names []string
	for _, tool := range s.tools() {
		names = append(names, tool.Tool.Name)
		if tool.Tool.Name == "refresh_paprika" {
			assert.Equal(t, "Reload recipes, categories and the meal plan from Paprika. Use when the user says they changed something in the Paprika app during this conversation.", tool.Tool.Description)
		}
	}
	assert.Contains(t, names, "refresh_paprika")
}

func TestWritesOnColdInstance(t *testing.T) {
	coldStartPlus := func(extra map[string]int) map[string]int {
		want := map[string]int{"ListCategories": 1, "ListMealTypes": 1, "ListMeals": 1, "ListRecipes": 1}
		for k, v := range extra {
			want[k] += v
		}
		return want
	}

	for name, tc := range map[string]struct {
		call func(*Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args map[string]interface{}
		want map[string]int
	}{
		"remove_meal": {
			call: func(s *Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return s.removeMeal
			},
			args: map[string]interface{}{"uid": "M2"},
			want: coldStartPlus(map[string]int{"ListMeals": 1, "SaveMeal": 1}),
		},
		"update_paprika_recipe": {
			call: func(s *Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return s.updateRecipe
			},
			args: map[string]interface{}{"uid": "R1", "notes": "Lekker"},
			want: coldStartPlus(map[string]int{"GetRecipe": 1, "SaveRecipe": 1}),
		},
		"add_meal": {
			call: func(s *Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return s.addMeal
			},
			args: map[string]interface{}{"date": "2025-03-10", "meal_type": "lunch", "name": "Tosti"},
			want: coldStartPlus(map[string]int{"SaveMeal": 1}),
		},
		"create_paprika_recipe": {
			call: func(s *Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return s.createRecipe
			},
			args: map[string]interface{}{"name": "Ravioli", "ingredients": "pasta", "directions": "koken"},
			want: coldStartPlus(map[string]int{"SaveRecipe": 1}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newFakeAccount()
			s := newOnDemandServer(client, storeWith(t, client), nil)

			out, isErr := callTool(t, tc.call(s), tc.args)
			assert.False(t, isErr, out)
			assert.Equal(t, tc.want, client.takeCalls())
		})
	}
}
