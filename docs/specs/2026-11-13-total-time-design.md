# Recipe total time in the MCP tools

## Intent

An agent writing recipes through this MCP reported that `create_paprika_recipe` has no `total_time` field, so a skill instruction to record total time couldn't be followed. Its three new recipes have only prep and cook times. Agents need to set, change and read back a recipe's total time the same way they handle prep and cook time.

## Current state

- `paprika.Recipe.TotalTime` exists (`internal/paprika/client.go`), is part of the sync hash and round-trips on the wire.
- `create_paprika_recipe` and `update_paprika_recipe` don't declare `total_time`, and their handlers never set it (`internal/mcpserver/server.go`).
- `Recipe.ToMarkdown` renders prep and cook time but not total time, so `get_recipe` can't show it.
- `update_paprika_recipe` starts from the stored recipe, so a total time set in the Paprika app already survives updates.

## Goals

- `create_paprika_recipe` accepts an optional `total_time` string and saves it.
- `update_paprika_recipe` accepts an optional `total_time`; passing it replaces the stored value, an explicit `""` clears it, and omitting it keeps it.
- `get_recipe` output shows `- **Total Time:** <value>` in the Details block when set.

## Non-goals

- `nutritional_info` has the same gap (in the model, not in the tools); out of scope.
- No parsing, normalizing or validating time strings.

## Anti-goals

- The server must not compute or overwrite total time from prep and cook time.

## Approaches

- **Expose the existing field (chosen):** add `total_time` as a free-text string to both tool schemas, the create handler, the update field map and the markdown renderer. Smallest change, mirrors `prep_time` and `cook_time` exactly.
- **Derive total from prep + cook on the server:** rejected. Paprika times are free text ("1 hr 20 mins"), and real totals often include resting or marinating time that neither field holds.
- **Have agents put total time in notes:** rejected. It defeats the structured field the Paprika app displays.

## Design

- **Schema:** in both tools, declare `total_time` directly after `cook_time`, described as "The total time for the recipe". On create it defaults to `""` like its siblings.
- **Create handler:** set `TotalTime: stringArg(args, "total_time")`.
- **Update handler:** add `"total_time": &recipe.TotalTime` to the field map.
- **Markdown:** include `r.TotalTime` in the Details-block condition and render it after Cook Time.

## Tests

- `TestCreateRecipe`: a passed `total_time` reaches the saved recipe.
- `TestUpdateRecipeKeepsExistingFields`: an omitted `total_time` keeps the stored value; an explicit `""` clears it.
- Markdown test: Total Time renders when set, and a recipe with only total time still gets a Details block.

## Left to the builder

- Exact test fixture values and whether the markdown case extends an existing test or adds one.
