package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/soggycactus/paprika-3-mcp/internal/cachestore"
	"github.com/soggycactus/paprika-3-mcp/internal/paprika"
)

// In onDemand mode the first tool call loads the cache from the store and syncs it with Paprika once.
// After that, reads are served from memory; Paprika is only read again by refresh_paprika and by the
// conflict checks before writes.

const (
	storeVersion = 1
	storeTimeout = 10 * time.Second
	// coldStartTimeout covers a first-ever load, which fetches every recipe at the client's rate limit
	coldStartTimeout = 15 * time.Minute
)

const (
	refreshToolDescription = "Reload recipes, categories and the meal plan from Paprika. Use when the user says they changed something in the Paprika app during this conversation."
	conflictMessage        = "This recipe was changed in Paprika since it was loaded. The latest version is now loaded; read it with get_recipe and retry."
	mealConflictMessage    = "This meal was changed in Paprika since it was loaded. The meal plan is now reloaded; check it with list_meals and retry."
)

// storeObject is the persisted cache. Meals are deliberately left out.
type storeObject struct {
	Version    int                       `json:"version"`
	Recipes    map[string]paprika.Recipe `json:"recipes"`
	Hashes     map[string]string         `json:"hashes"`
	Categories []paprika.Category        `json:"categories"`
	MealTypes  []paprika.MealType        `json:"meal_types"`
}

// loadAttempt is one cold-start load; err is set before done is closed
type loadAttempt struct {
	done chan struct{}
	err  error
}

func (c *recipeCache) enableOnDemand(store cachestore.Store) {
	c.onDemand = true
	c.store = store
}

// ensureLoaded waits for the cold-start load, starting it if needed. The load runs detached from ctx,
// so a caller that gives up doesn't abort it; a failed load is retried by the next caller.
func (c *recipeCache) ensureLoaded(ctx context.Context) error {
	c.loadMu.Lock()
	if c.coldLoaded {
		c.loadMu.Unlock()
		return nil
	}
	attempt := c.loading
	if attempt == nil {
		attempt = &loadAttempt{done: make(chan struct{})}
		c.loading = attempt
		go c.coldStart(context.WithoutCancel(ctx), attempt)
	}
	c.loadMu.Unlock()

	select {
	case <-attempt.done:
		if attempt.err != nil {
			return fmt.Errorf("failed to load recipes from Paprika: %w", attempt.err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("failed to load recipes from Paprika: %w", ctx.Err())
	}
}

func (c *recipeCache) coldStart(ctx context.Context, attempt *loadAttempt) {
	ctx, cancel := context.WithTimeout(ctx, coldStartTimeout)
	defer cancel()

	attempt.err = c.loadAndSync(ctx)

	c.loadMu.Lock()
	c.coldLoaded = attempt.err == nil
	c.loading = nil
	c.loadMu.Unlock()
	close(attempt.done)
}

func (c *recipeCache) loadAndSync(ctx context.Context) error {
	c.loadStore(ctx)
	if err := c.refreshMeals(ctx); err != nil {
		return err
	}
	if _, err := c.refresh(ctx, 0); err != nil {
		c.mu.RLock()
		loaded := c.loaded
		c.mu.RUnlock()
		if !loaded {
			return err
		}
		c.logger.Warn("using partly stale recipe cache", "err", err)
	}
	c.saveStore(ctx)
	return nil
}

// refreshAll re-syncs meal types, meals, categories and changed recipes, and returns how many
// recipes were added or changed
func (c *recipeCache) refreshAll(ctx context.Context) (int, error) {
	if err := c.refreshMeals(ctx); err != nil {
		return 0, err
	}
	changed, err := c.refresh(ctx, 0)
	c.saveStore(ctx)
	return len(changed), err
}

func (c *recipeCache) refreshMeals(ctx context.Context) error {
	mealTypes, err := c.client.ListMealTypes(ctx)
	if err != nil {
		return err
	}
	meals, err := c.client.ListMeals(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.mealTypes, c.meals = mealTypes.Result, meals.Result
	c.mu.Unlock()
	return nil
}

// loadStore restores the cache from the store. A missing, unreadable or outdated object is not
// fatal: the following refresh then fetches every recipe.
func (c *recipeCache) loadStore(ctx context.Context) {
	if c.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	data, generation, err := c.store.Load(ctx)
	switch {
	case errors.Is(err, cachestore.ErrNotFound):
		c.logger.Info("no stored recipe cache yet")
		return
	case err != nil:
		c.logger.Warn("failed to load stored recipe cache", "err", err)
		return
	}

	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	c.generation = generation

	var obj storeObject
	if err := json.Unmarshal(data, &obj); err != nil || obj.Version != storeVersion || obj.Recipes == nil || obj.Hashes == nil {
		c.logger.Warn("ignoring unreadable stored recipe cache", "version", obj.Version, "err", err)
		return
	}
	c.mu.Lock()
	c.recipes, c.hashes, c.categories, c.mealTypes = obj.Recipes, obj.Hashes, obj.Categories, obj.MealTypes
	c.loaded = true
	c.mu.Unlock()

	if c.saved, err = c.marshalStore(); err != nil {
		c.saved = nil
	}
	c.logger.Info("loaded stored recipe cache", "recipes", len(obj.Recipes), "generation", generation)
}

func (c *recipeCache) marshalStore() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return json.Marshal(storeObject{
		Version:    storeVersion,
		Recipes:    c.recipes,
		Hashes:     c.hashes,
		Categories: c.categories,
		MealTypes:  c.mealTypes,
	})
}

// saveStore writes the cache to the store if it changed since the last load or save. It never
// overwrites an object another writer changed: a failed generation precondition is only logged.
func (c *recipeCache) saveStore(ctx context.Context) {
	if c.store == nil {
		return
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	data, err := c.marshalStore()
	if err != nil {
		c.logger.Warn("failed to encode recipe cache", "err", err)
		return
	}
	if bytes.Equal(data, c.saved) {
		return
	}

	// Detached so the save completes even if the client hangs up after a successful write
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	generation, err := c.store.Save(ctx, data, c.generation)
	switch {
	case errors.Is(err, cachestore.ErrPrecondition):
		c.logger.Warn("stored recipe cache changed elsewhere (generation precondition failed); not overwriting it", "generation", c.generation)
	case err != nil:
		c.logger.Warn("failed to save recipe cache", "err", err)
	default:
		c.generation, c.saved = generation, data
		c.logger.Info("saved recipe cache", "generation", generation)
	}
}

// changedSinceLoad reports whether a freshly fetched recipe differs from the cached copy. Both the
// stored hash and the content hash are compared, because Paprika's apps may save edits without
// updating the stored hash.
func (c *recipeCache) changedSinceLoad(fetched paprika.Recipe) (bool, error) {
	c.mu.RLock()
	cached, ok := c.recipes[fetched.UID]
	c.mu.RUnlock()
	if !ok || cached.Hash != fetched.Hash {
		return true, nil
	}
	cachedHash, err := cached.ComputeHash()
	if err != nil {
		return false, err
	}
	fetchedHash, err := fetched.ComputeHash()
	if err != nil {
		return false, err
	}
	return cachedHash != fetchedHash, nil
}

// checkRecipeConflict returns a tool result when the recipe must not be updated. On a conflict,
// the fetched version replaces the cached one so the caller can read it and retry.
func (s *Server) checkRecipeConflict(ctx context.Context, fetched paprika.Recipe) (*mcp.CallToolResult, error) {
	if fetched.InTrash {
		return mcp.NewToolResultError("This recipe is in the Paprika trash; restore it in the app first."), nil
	}
	changed, err := s.cache.changedSinceLoad(fetched)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	s.cache.put(fetched)
	s.cache.saveStore(ctx)
	s.logger.Info("Refused update of recipe changed in Paprika", "uid", fetched.UID)
	return mcp.NewToolResultError(conflictMessage), nil
}

// mealUnchanged compares the cached meal with the one in a fresh meal list. On a difference, the
// fresh list replaces the cached meals and it returns false.
func (c *recipeCache) mealUnchanged(uid string, fresh []paprika.Meal) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, freshMeal := findMeal(c.meals, uid), findMeal(fresh, uid)
	if reflect.DeepEqual(cached, freshMeal) {
		return true
	}
	c.meals = fresh
	return false
}

func findMeal(meals []paprika.Meal, uid string) *paprika.Meal {
	for i := range meals {
		if meals[i].UID == uid {
			return &meals[i]
		}
	}
	return nil
}

// upsertMeal records a meal that was just saved
func (c *recipeCache) upsertMeal(meal paprika.Meal) {
	if !c.onDemand {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	meals := append([]paprika.Meal(nil), c.meals...)
	if existing := findMeal(meals, meal.UID); existing != nil {
		*existing = meal
	} else {
		meals = append(meals, meal)
	}
	c.meals = meals
}

// mealPlan returns all meals and meal types: from memory in onDemand mode, otherwise live from Paprika
func (s *Server) mealPlan(ctx context.Context) ([]paprika.Meal, []paprika.MealType, error) {
	if !s.cache.onDemand {
		meals, err := s.paprika3.ListMeals(ctx)
		if err != nil {
			return nil, nil, err
		}
		mealTypes, err := s.paprika3.ListMealTypes(ctx)
		if err != nil {
			return nil, nil, err
		}
		return meals.Result, mealTypes.Result, nil
	}
	if err := s.cache.ensureLoaded(ctx); err != nil {
		return nil, nil, err
	}
	s.cache.mu.RLock()
	defer s.cache.mu.RUnlock()
	return append([]paprika.Meal(nil), s.cache.meals...), append([]paprika.MealType(nil), s.cache.mealTypes...), nil
}

func (s *Server) mealTypes(ctx context.Context) ([]paprika.MealType, error) {
	if !s.cache.onDemand {
		mealTypes, err := s.paprika3.ListMealTypes(ctx)
		if err != nil {
			return nil, err
		}
		return mealTypes.Result, nil
	}
	_, mealTypes, err := s.mealPlan(ctx)
	return mealTypes, err
}

// categoryError explains an unknown category; in onDemand mode the category may be newer than the cache
func (s *Server) categoryError(err error) *mcp.CallToolResult {
	msg := err.Error()
	if s.cache.onDemand {
		msg += ". If a category was just created in the Paprika app, run refresh_paprika and try again."
	}
	return mcp.NewToolResultError(msg)
}

func (s *Server) refreshTool() server.ServerTool {
	return server.ServerTool{
		Tool:    mcp.NewTool("refresh_paprika", mcp.WithDescription(refreshToolDescription)),
		Handler: s.refreshPaprika,
	}
}

func (s *Server) refreshPaprika(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	if err := s.cache.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	changed, err := s.cache.refreshAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh from Paprika: %w", err)
	}

	s.cache.mu.RLock()
	recipes, categories, mealTypes := len(s.cache.recipes), len(s.cache.categories), len(s.cache.mealTypes)
	meals := 0
	for _, m := range s.cache.meals {
		if !m.Deleted {
			meals++
		}
	}
	s.cache.mu.RUnlock()

	s.logger.Info("Refreshed from Paprika", "recipes", recipes, "changed", changed, "duration", time.Since(start))
	return mcp.NewToolResultText(fmt.Sprintf("Reloaded from Paprika: %d recipes (%d added or changed), %d categories, %d meal types, %d planned meals.",
		recipes, changed, categories, mealTypes, meals)), nil
}
