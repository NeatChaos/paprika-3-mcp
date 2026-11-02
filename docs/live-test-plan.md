# Live test plan: remote Paprika connector

Run this from a Claude client connected to the remote connector (`https://paprika-mcp-proxy.<subdomain>.workers.dev/mcp`). It covers what coded tests cannot: the real Paprika API, the real apps, and Cloudflare Access. Conflict refusal is covered by coded tests and is not repeated here.

## Rules for the session

- Use only the remote Paprika connector. Disable any local Paprika MCP server first so no step writes through it.
- Every recipe or meal you create has a name starting with `[TEST]`. Never modify, move or delete anything without that prefix, except the cleanup in step 7.
- Stop and report on the first unexpected result. Do not retry writes.
- Steps marked **Owner** need the person at a phone or the Mac app; ask them and wait.

## 0. Preconditions (Owner)

- [ ] A recipe export exists: Paprika for Mac, File → Export, All Recipes, Paprika Recipe Format. Meals are not in the export; Cloud Sync holds them.
- [ ] The connector is signed in through Cloudflare Access.

## 1. Reads

- [ ] `list_categories` returns the category tree with counts.
- [ ] `search_recipes` with `limit: 1000` and no filters reports the library size (about 540).
- [ ] `get_recipe` on any one result returns ingredients, directions and categories.
- [ ] `list_meals` for this week returns the planner without errors.

## 2. Create and read back

- [ ] `create_paprika_recipe` named `[TEST] Connector check`, with short ingredients and directions (unnumbered) and one existing category.
- [ ] `get_recipe` by the returned uid: every field matches what was sent.
- [ ] `refresh_paprika` reports 0 changed recipes (what Paprika stored equals what was sent).
- [ ] **Owner:** the recipe appears on the phone after sync, with the right category.

## 3. Update

- [ ] `update_paprika_recipe` on the `[TEST]` recipe, setting `notes` to `edited by connector`.
- [ ] `get_recipe` shows the new notes and the unchanged category.
- [ ] `refresh_paprika` reports 0 changed recipes.
- [ ] **Owner:** the phone shows the new notes.

## 4. Edits made in the app

- [ ] **Owner:** on the phone, change the `[TEST]` recipe's notes to `edited on phone`, and wait for sync.
- [ ] `refresh_paprika` reports at least 1 changed recipe.
- [ ] `get_recipe` shows `edited on phone`.

If the refresh reports 0 changes, record it: Paprika did not change the stored hash for an app edit, so `refresh_paprika` cannot see such edits.

## 5. Meal planner

- [ ] `add_meal` with `name: "[TEST] dinner"`, `meal_type: "dinner"`, on a date next week.
- [ ] `add_meal` with `recipe` set to the `[TEST]` recipe's uid, `meal_type: "lunch"`, same date.
- [ ] If the account has a custom meal type (not breakfast, lunch, dinner or snack), `add_meal` with `name: "[TEST] custom"` and that type.
- [ ] `list_meals` for that date shows all of them with the right types.
- [ ] **Owner:** the phone shows each meal under the right type, including the custom one.
- [ ] `remove_meal` each `[TEST]` meal by its uid; `list_meals` no longer shows them.
- [ ] **Owner:** the phone no longer shows them, and the `[TEST]` recipe still exists.

## 6. Delete

- [ ] `delete_paprika_recipe` on the `[TEST]` recipe.
- [ ] `search_recipes` for `[TEST]` finds nothing.
- [ ] `delete_paprika_recipe` on the same uid again returns "already in the Paprika trash".
- [ ] **Owner:** the recipe is in the app's trash and restores from there.

## 7. Cleanup

- [ ] `delete_paprika_recipe` on the earlier test recipe `ZZ MCP Test – Lemon Tahini Dressing`.
- [ ] **Owner:** empty the trash in the app if desired.

## Report

For each step: pass or fail, and the exact tool output on any failure. Call out step 4 and the custom meal type in step 5 explicitly; those are the open questions.
