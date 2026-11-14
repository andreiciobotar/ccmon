# The monitor reports cost in money

## Outcome
Everywhere the CLI, its JSON and the dashboard report tokens, they also report cost, computed from a
price file the human can edit. Cost becomes the headline where a share or a ranking is shown; token
counts stay visible. A model with no price is never counted as zero silently.

## Facts (established 2026-11-14)
Full count over `~/.claude/projects`: 788 files, 431,444 lines, 51,284 deduplicated assistant messages.

| `message.model` | msgs | input | output | cache write (5m / 1h) | cache read |
|---|---|---|---|---|---|
| claude-opus-5-5 | 50,095 | 105,871 | 46,085,797 | 256,259,025 / 54,215,514 | 11,634,338,619 |
| claude-fable-5-2 | 507 | 1,204 | 510,040 | 0 / 1,796,804 | 90,399,712 |
| claude-sonnet-5-5 | 442 | 906 | 695,680 | 1,991,203 / 87,042 | 51,522,743 |
| claude-haiku-5-5 | 240 | 492 | 299,909 | 1,051,166 / 74,074 | 11,263,300 |

- No other model id occurs (`<synthetic>` lines, 34, are already skipped by the parser).
- Every message carries `usage.cache_creation.ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`,
  and in all 51,284 they add up to `cache_creation_input_tokens`. The two tiers have different prices.
- Claude Haiku 5.5 is priced per request by prompt length (input + cache write + cache read): over
  100,000 tokens every class costs 5x. 22 of the 240 Haiku messages are over. No other corpus model has a
  long-context surcharge (the 1M window is at standard price).
- Fields that could change the price but never do in the corpus: `speed` is `standard` in all messages
  (fast mode costs 2x), `inference_geo` is `global` or `not_available` (`us` costs 1.1x),
  `server_tool_use.web_search_requests` sums to 0 ($10 per 1,000), `service_tier` is `standard`.
  **Not priced** (nothing to verify against); listed in the price file's header and the README as limits.
- `output_tokens_details.thinking_tokens` is part of `output_tokens`, not an extra charge.
- 127 lines of type `cost-state` carry Claude Code's own `totalCostUSD` and per-model `costUSD`. One was
  recomputed by hand from the list prices below and matched to the last digit. They cover few sessions, so
  they are a cross-check, not a source.
- Prices: https://platform.claude.com/docs/en/about-claude/pricing, read 2026-11-14, USD per million tokens:

| model | input | cache write 5m | cache write 1h | cache read | output |
|---|---|---|---|---|---|
| claude-fable-5-2 | 8 | 10 | 16 | 0.20 | 40 |
| claude-opus-5-5 | 4 | 5 | 8 | 0.20 | 20 |
| claude-sonnet-5-5 | 2 | 2.50 | 4 | 0.10 | 10 |
| claude-haiku-5-5, prompt ≤ 100,000 | 0.10 | 0.125 | 0.20 | 0.01 | 0.50 |
| claude-haiku-5-5, prompt > 100,000 | 0.50 | 0.625 | 1 | 0.05 | 2.50 |

## Design

### Price file: `ccmon/prices.toml`
Shipped next to the code that reads it (the monitor runs from its checkout; the factory plugin is a
separate thing). TOML because it takes comments and Python 3.11 reads it with the standard library.
`--prices PATH` on every command reads another file instead (private rates, tests); `ccmon-tui --prices`
passes it through, like `--projects-dir`.

```toml
# header comment: what is not priced (fast mode, US-only inference, web search), how to edit
currency = "USD"
as_of = "2026-11-14"
source = "https://platform.claude.com/docs/en/about-claude/pricing"
# per million tokens

[models.claude-opus-5-5]
input = 4
output = 20
cache_write_5m = 5
cache_write_1h = 8
cache_read = 0.20

[models.claude-haiku-5-5]
...
[models.claude-haiku-5-5.long_prompt]   # optional; replaces the five rates for one request
over = 100000                            # when input + cache write + cache read exceeds this
input = 0.50
...
```

Loading (new module `ccmon/prices.py`, the one owner of prices and the cost of one message):
- `load(path=None)` returns the parsed prices; a missing file, invalid TOML, a missing or empty
  `currency`/`as_of`, a model missing one of the five rates, a `long_prompt` missing `over` or a rate, or a
  rate that is not a non-negative number is a `SessionError` naming the file and the problem (exit code 2).
- A model id matches a `[models.X]` table by exact name, or by its name with a trailing `-YYYYMMDD` removed.
  Nothing else (no prefix matching: a wrong price is worse than a missing one).
- `cost(model, raw_usage)` returns the cost of one message, or `None` when the model has no price.
  Cache write split: `1h = min(ephemeral_1h_input_tokens, cache_creation_input_tokens)`, the rest at the 5m
  rate; so a usage block without the breakdown is priced entirely at the 5m rate.

### Where cost is computed
Per message, in the parser, because two rules are per request (cache tier, Haiku prompt length).
`transcript.load_session(path, prices)` passes prices down to where usage is accumulated. `Usage` gains two
fields that add like the token classes:
- `cost`: money, unrounded float, for messages whose model has a price.
- `unpriced`: tokens (all four classes) of messages whose model has no price. Their cost is not in `cost`.

Nothing else computes cost; aggregation is addition.

### JSON
- Every usage object (`usage`, `by_model`, `by_agent`, `usage_by_model` values) gains `"cost"` (rounded to 6
  decimals) and `"unpriced"` (tokens).
- Wherever `"tokens": {total, main, subagents, main_pct, subagent_pct}` appears (session, task, repo), a
  sibling `"cost"` with the same five keys by money and, inside it, `"unpriced_tokens"` and `"unpriced_models"`
  (sorted ids with `unpriced > 0`). Percentages are of priced cost.
- `agents` rows gain `cost_p50`, `cost_p90` (same population as `tokens_p50`); the `agents` document gains
  top-level `unpriced_models`.
- Each document (`sessions`, `session`, `agents`) gains top-level
  `"prices": {"currency", "as_of", "source", "path"}`.
- Existing keys keep their meaning. Token percentages stay.

### CLI text
One `money(x, currency)` formatter: `$` prefix for USD, otherwise the code and a space. `0` → `$0`;
under 0.00005 → `<$0.0001`; under 0.01 → four decimals (`$0.0012`); under 100 → two decimals; under 10,000 → whole units;
then `k`, `M` with one decimal. A figure whose usage has `unpriced > 0` gets a trailing `*`.
- `sessions`: columns `cost`, `main%`, `sub%` (now by cost), `tokens` (was `total`), then as before.
- `session`: a `cost` line above `tokens` (total, main, subagents with percentages by cost); a `cost`
  column in By model, By agent and Dispatches.
- `agents`: columns `cost`, `cost p50`, `cost p90` added; token columns stay.
- Footer on all three: `prices: USD as of 2026-11-14 (<path>)`. When anything in the output is unpriced, one
  more line: `* no price for: <models> (<N> tokens); cost excludes them`. By model shows `unpriced` as the
  cost of such a model.

### Dashboard (Go)
`data.Usage` gains `Cost`, `Unpriced`; a `Cost` struct mirrors `Tokens`; results carry `Prices`. One
`Money` formatter with the rules above. Group rows sum cost the way they sum tokens today.
- Dashboard tiles: first tile `COST` (money; sub-line: tokens). `SUBAGENT SHARE` is by cost.
- Sessions table: `COST` column added before `TOKENS`; when the width cannot hold both, `TOKENS` drops.
  `MAIN│SUB` bar by cost. Sort mode `tokens` becomes `cost`; `share` is share of cost.
- Agent mix and Model mix by cost; Model mix shows money per model (`unpriced` for a model with no price).
- Now running: cost so far, then tokens.
- Session view: header line leads with cost, then tokens; main and subagents as money with percentages by
  cost; the four token classes stay. Stacked bar and legend by cost. Dispatch table gains `COST` (the last
  numeric column to drop); detail pane gains a cost line under `total`.
- Agents view: model bar by cost; each agent's usage line ends `= <tokens> · <cost>`; the p50/p90 bars show
  cost instead of tokens (token percentiles stay in JSON and the CLI).
- Unpriced: a figure with unpriced tokens carries `*`; when anything in range is unpriced the dashboard
  subtitle line appends, in the warning colour, `no price for <models>: excluded from cost`.
- Help view: one line saying where prices come from (the loaded file's path, currency, as-of date).
- Look at rendered output: `make screenshot` for dashboard, session, agents at 140x40 and 80x24.

### Docs
README: cost in the command list, the price file (location, format, `--prices`, how unpriced models show,
what is not priced). `docs/specs/tui.md`: the lines describing tiles, table, mixes, session header, agents.

## Tests
- `prices`: default file loads and prices the four corpus models; a hand-computed message per rule (plain,
  1h/5m split, no breakdown, Haiku at 100,000 and 100,001, dated id suffix, unknown model → None); each
  load error.
- Parser/metrics with `tests/fixtures/prices.toml` (round rates, one fixture model left out): exact costs
  per session, dispatch, agent, model; main/subagent cost split; the unpriced model appears in
  `unpriced_models`, its tokens in `unpriced`/`unpriced_tokens`, and totals equal the sum of priced parts;
  repo and task groups sum their sessions.
- CLI: text output shows cost columns, footer, and the `*` line only when something is unpriced; JSON keys.
- Go: decode of the new fields; `Money`; goldens regenerated (they render the fixtures through the CLI with
  `tests/fixtures/prices.toml`, so they show cost and an unpriced model); sort by cost with a pair whose
  cost order differs from its token order; unit tests on fakes for dollar-size figures.

## Defaults chosen
- Sub-cent amounts show four decimals rather than `<$0.01`, so small sessions still rank; the widest money
  string is then 9 cells (`<$0.0001*`), which every money column holds without truncation.
- Cost is list price at the time of reading the file, not the price on the day of the message; the file has
  one `as_of`.
- Subscription plans are not modelled: cost is the API list-price equivalent.

## Not in scope
Fast mode, US-only inference, web search and batch pricing (absent from the corpus); price history;
currency conversion; the kaizen stats page; `factory/`.
