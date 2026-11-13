# Recipe Total Time Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let MCP clients set, change, clear and read back a recipe's `total_time`.

**Architecture:** `paprika.Recipe.TotalTime` already exists, is hashed and round-trips on the wire. This plan exposes it in the `create_paprika_recipe` and `update_paprika_recipe` tool schemas and handlers, and renders it in `Recipe.ToMarkdown` so `get_recipe` shows it.

**Tech Stack:** Go, `github.com/mark3labs/mcp-go`, testify.

**Spec:** `docs/specs/2026-11-13-total-time-design.md`

## Global Constraints

- `total_time` is a free-text string, never parsed, validated or derived from prep and cook time.
- Its schema description is exactly `The total time for the recipe`, declared directly after `cook_time` in both tools.
- Markdown line format is exactly `- **Total Time:** <value>`, rendered after Cook Time.
- Zero warnings from `go vet` and the repo's prek hooks.

## Review Focus

- **Schema advertises the field:** an agent only learns of `total_time` from `tools/list`; handler tests alone would pass with the schema line missing. Task 2 pins it with a schema test.
- **Update without `total_time`:** a stored total time must survive an update that changes other fields. Task 2 tests this.
- **Explicit empty string on update:** `"total_time": ""` clears the stored value. Task 2 tests this.
- **Details block with only total time:** a recipe with no servings, prep, cook or difficulty but a total time must still render the Details block. Task 1 tests this.
- **Non-string `total_time`:** a number such as `45` is ignored, matching how `stringArg` and the update field map treat every sibling field. No new test; behavior is unchanged shared code.

______________________________________________________________________

### Task 1: Render total time in recipe markdown

**Files:**

- Modify: `internal/paprika/client.go` (`Recipe.ToMarkdown`, Details block)
- Test: `internal/paprika/unit_test.go`

**Interfaces:**

- Consumes: nothing new.

- Produces: `ToMarkdown()` output contains `- **Total Time:** <value>\n` inside `## Details` when `TotalTime != ""`. Task 2's create test asserts on this string.

- [ ] **Step 1: Write the failing tests**

```go
func TestToMarkdownShowsTotalTime(t *testing.T) {
	r := Recipe{Name: "Stew", PrepTime: "15 min", CookTime: "2 hr", TotalTime: "2 hr 30 min"}
	assert.Contains(t, r.ToMarkdown(), "- **Cook Time:** 2 hr\n- **Total Time:** 2 hr 30 min\n")

	onlyTotal := Recipe{Name: "Bread", TotalTime: "4 hr"}
	assert.Contains(t, onlyTotal.ToMarkdown(), "## Details\n- **Total Time:** 4 hr\n")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/paprika -run TestToMarkdownShowsTotalTime` Expected: FAIL, output lacks `Total Time`.

- [ ] **Step 3: Implement**

Add `r.TotalTime != ""` to the Details-block condition and print the Total Time line after Cook Time, before Difficulty.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/paprika` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/paprika/client.go internal/paprika/unit_test.go
git commit -m "feat: show total time in recipe markdown"
```

### Task 2: Accept `total_time` in create and update tools

**Files:**

- Modify: `internal/mcpserver/server.go` (`tools()` schemas for `create_paprika_recipe` and `update_paprika_recipe`; `createRecipe` struct literal; `updateRecipe` field map)
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**

- Consumes: Task 1's `- **Total Time:** <value>` markdown line.

- Produces: tool argument `total_time` (string) on both tools.

- [ ] **Step 1: Write the failing tests**

Schema test, new top-level function in `server_test.go`:

```go
func TestRecipeToolsAcceptTotalTime(t *testing.T) {
	s := newTestServer(newFakeAccount())
	for _, tool := range s.tools() {
		if tool.Tool.Name == "create_paprika_recipe" || tool.Tool.Name == "update_paprika_recipe" {
			assert.Contains(t, tool.Tool.InputSchema.Properties, "total_time", tool.Tool.Name)
		}
	}
}
```

New subtest in `TestCreateRecipe`, `"total time is saved"`: call `createRecipe` with `name`, `ingredients`, `directions` and `"total_time": "45 min"`; assert not an error, `client.savedRecipe[0].TotalTime == "45 min"`, and output contains `- **Total Time:** 45 min`.

In `TestUpdateRecipeKeepsExistingFields`, set `r.TotalTime = "1 hr"` alongside the other seeded fields, and add `assert.Equal(t, "1 hr", saved.TotalTime)`.

New test:

```go
func TestUpdateRecipeTotalTime(t *testing.T) {
	for _, tc := range []struct{ name, arg, want string }{
		{"replaces", "50 min", "50 min"},
		{"empty string clears", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newFakeAccount()
			r := client.recipes["R1"]
			r.TotalTime = "1 hr"
			client.recipes["R1"] = r
			s := newTestServer(client)

			_, isErr := callTool(t, s.updateRecipe, map[string]interface{}{"uid": "R1", "total_time": tc.arg})
			assert.False(t, isErr)
			require.Len(t, client.savedRecipe, 1)
			assert.Equal(t, tc.want, client.savedRecipe[0].TotalTime)
		})
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/mcpserver -run 'TestRecipeToolsAcceptTotalTime|TestCreateRecipe|TestUpdateRecipe'` Expected: FAIL on schema property, saved `TotalTime`, and the replace/clear cases. `TestUpdateRecipeKeepsExistingFields` already passes; it guards against regressions.

- [ ] **Step 3: Implement**

- In both tool definitions, after `cook_time`, add `mcp.WithString("total_time", mcp.Description("The total time for the recipe"))`, with `mcp.DefaultString("")` on the create tool only, matching its siblings.

- In `createRecipe`, set `TotalTime: stringArg(args, "total_time")` after `CookTime`.

- In `updateRecipe`, add `"total_time": &recipe.TotalTime` to `fields` after `cook_time`.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./... && go vet ./...` Expected: PASS, no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/server.go internal/mcpserver/server_test.go
git commit -m "feat: accept total_time in create and update recipe tools"
```
