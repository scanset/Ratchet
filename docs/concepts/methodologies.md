# Development methodologies (and what generated code teaches us)

There is more than one way to drive verifiable code generation in Ratchet. Each is an arrangement of
`generate` steps plus a deterministic Oracle - not a philosophy, a topology. This page covers the two
that are proven, the ladder that unifies them, and the engineering lessons that hold across both. For the
multi-file composition mechanics specifically, see [Composition](composition.md).

## Two methodologies

**Spec-driven** (the `compose` model). A folder of specs becomes a working system: build the units in
dependency order, each generated against the code already built, each Oracle-checked (it must build)
before the next. The test is generated alongside the implementation. See [Composition](composition.md).

**Test-driven** (the assurance-ladder model). The test is authored FIRST, from the spec's behavior, and
becomes the contract the implementation must satisfy:

1. **Stub** - signatures only, `panic` bodies. Oracle: it compiles (the type-driven rung).
2. **Test (red)** - author the test (and a property/fuzz target). Oracle: it COMPILES against the stub
   AND FAILS. A test that passes against panic bodies asserts nothing and is rejected.
3. **Impl (green)** - fill the bodies. Oracle: `go test -race` passes. Failures feed back as repair.
4. **Harden** - the full gate (vet/staticcheck/govulncheck).

The difference that matters: spec-driven trusts a test generated next to the code; test-driven proves the
test is meaningful (red) before any code exists, then drives the code to green. Test-driven costs more
steps and buys more assurance.

## The assurance ladder

The methodologies are rungs of one ladder of increasing Oracle strength over the same code:

```
types (compiler) -> examples (go test) -> properties (fuzz / -race) -> harden (vet/staticcheck/govulncheck)
```

A task climbs as far as it needs. A plain data helper stops at types+examples; a concurrent type climbs
to properties; anything shipped gets hardened. The property/fuzz rung is the one that catches what a build
gate cannot: `go test -fuzz` and `-race` exercise the UNEXERCISED paths - the gap behind "it compiles and
the happy-path test passes, so it must be correct". (This is exactly the class of bug - an expiry race, a
large-count overflow - that slips through a build-only gate.)

## Lessons that hold across both

- **Green means "won't break", not "is correct".** An Oracle pass certifies the verified properties, not
  intent. The stronger the rung, the closer green gets to correct - which is why the ladder exists.
- **A meaningful test must be proven, not assumed.** Generating test and code together lets a weak test
  pass trivially (false green). The red gate - compile-then-fail against a stub - is what gives a test
  teeth. If you only take one idea from test-driven, take this.
- **Determinism beats prompts for mechanical errors.** A small local model will violate explicit prompt
  rules ("import only what you use", "one package", "no entry point here"). Do not rely on the model's
  care for mechanical correctness - fix the class deterministically (e.g. strip unused imports after
  generation) so every flow benefits.
- **Bind the real contract; don't ask the model to remember it.** Put the already-built API in front of
  the model verbatim (the built code wins over the spec). The same instinct drives the red gate (the
  stub is the contract the test must match) and Composition's `project_api`.
- **Bound the model; fail clean; ship nothing broken.** Make a strict Oracle productive with a feedback
  cycle (re-generate with the verdict), and bound it with a repair cap (K attempts, then a clean failure)
  so an unsatisfiable gate fails fast instead of spinning. Across hard runs the win is not a perfect
  success rate - it is that the Oracle rejects every bad attempt and ships nothing. That guarantee is the
  product.
- **Repair must EDIT, not restart.** A repair step that sees only the Oracle's verdict - not the artifact
  that produced it - cold-starts every attempt and plays whack-a-mole (a different trivial error each try).
  Feed the previous failing output back in alongside the verdict and instruct "edit this to fix exactly
  what the verdict flags; keep everything else". This one change moves the reliability frontier further
  than most prompt tuning - much of what looks like a capability ceiling is an under-built repair loop.
- **A pass can still be wrong where the test is silent.** The Oracle is only as strong as the test that
  drives it. A bug on a path the test never exercises - e.g. a map write under a read-lock that the test's
  data never triggers concurrently - sails through vet, `-race`, fuzz, and lint and still ships; `-race`
  only flags races that actually run. When correctness matters, "passed the Oracle" means "ready for
  review", not "proven correct": route an independent reader, not more generation.
- **Pin the contract in the spec - an ambiguous spec masquerades as a model failure.** When generation
  stalls on a behavior the spec left open (which of two valid encodings? what happens at the zero boundary?)
  the model is not failing - it is guessing, and its guess disagrees with the test's guess. State the exact
  rule and the invariant the Oracle will check. The decisions only you can make belong in the spec, not in
  the repair loop.

## The capability frontier: which residual are you in?

Reliability falls as the number of interdependent pieces a step must hold at once rises (Composition's
multi-reference frontier is the same effect). But "the model failed" is not one thing. When a step stalls,
diagnose WHICH residual it is - each routes to a different, cheaper-than-you-think fix, and only the last
needs a bigger model:

1. **Structural-coherence wall -> remove the choice.** A multi-type system fails when the model scatters
   the pieces across inconsistent packages so they cannot reference each other. The fix is structure, not a
   bigger model: one package at the root, bind the real API, prefer interfaces, keep a type's data and
   methods in one unit. These push the ceiling up without spending capability.
2. **Under-specified spec -> you specify.** The step is guessing at a behavior the spec left open, and its
   guess disagrees with the test's guess. Pin the rule and the invariant (above). This is the cheapest
   residual - a few lines of spec - and it is *not* a model limitation.
3. **Under-built repair / missing authoring rule -> fix the loop.** A complex artifact (a concurrent,
   fuzzed, multi-type test) that fails *differently* each attempt looks like a ceiling but usually is not.
   Wire the prior artifact into repair (edit-don't-restart), ground the failing phase on the relevant
   idioms, and add rules that delete whole error classes (e.g. "a total function is never an error case";
   "check inverse pairs by round-trip, not literals"). With these, a small local model reaches green on
   exactly the artifacts that defeated it before. Most apparent ceilings live here.
4. **Genuine capability wall -> escalate this one node.** Some tasks need a structural move the model
   cannot perform even when shown - e.g. reconciling a type's public method signature with a library
   interface's required signature by splitting into a wrapper type. The model recognizes the constraint and
   still cannot execute the refactor: the SAME error, same line, every attempt, with the correct solution
   already in the prompt. Grounding does not help - knowledge is not the gap. This, not "complex code", is
   the "bigger / reasoning model" line, and the one place targeted escalation (hand just the stuck node to a
   stronger model) earns its keep.

The practical takeaway: spend structure and specification first - that is where most failures actually live
- fix the repair loop before blaming the model, and treat a stronger model as a deliberate choice for the
restructuring residual (4), not the default reach. And remember residual #4's quiet cousin: a step can
*pass* and still be wrong where the test is silent - that one routes to review, not to any model.
