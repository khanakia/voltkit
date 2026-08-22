# docs

Design notes, proposals, and planned work for the kit.

| Path | Holds |
|---|---|
| [`roadmap.md`](./roadmap.md) | what is deferred and what is planned, across every module |
| [`proposals/`](./proposals) | a design worked out in full but not built — one file per proposal |

## What belongs here

**Proposals** are for work that was designed, discussed, and consciously *not* built — whether deferred or rejected outright. A proposal exists so the reasoning survives: the next person should not have to re-derive why an API takes the shape it does, or re-discover the tradeoff that settled it.

A **rejected** proposal is worth more than a deferred one, because a rejected idea is the one most likely to be proposed again. It records what was measured, what was tried, and what would have to change for the answer to be different.

Each one records the problem, the proposed API, the rule or heuristic it depends on, and — most importantly — **the open questions that stopped it shipping**. A proposal with no open questions should just be built.

**The roadmap** is the index: one line per item, pointing at its proposal when there is one.

## What does not belong here

- Per-module usage documentation — that lives in each module's own `README.md`, beside the code it describes
- Internal design records for work already shipped — those live in `docsi/`
- Task tracking — a proposal is a design, not a checklist

## Adding a proposal

Name the file `<module>-<feature>.md`, add a line to the roadmap, and state the open questions honestly. A proposal that hides its unresolved parts is worse than none, because it reads as ready to implement.
