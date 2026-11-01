# JEV copy evaluation

Run the deterministic extraction regression set without credentials or network calls:

```sh
env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$(mktemp -d)" \
  go test -short ./internal/ui -run 'Test(Copy|AskJevCopy|LatestAssistantText|JevCopyCorpus)'
```

`testdata/jevcopy.json` contains 28 manually labelled cases adapted from historical
assistant replies across 17 topics. Operational identifiers, addresses, products,
and destinations were replaced with placeholders before inclusion. Raw transcripts
and the private source mapping are not part of the repository. The labels describe
what should be copyable; they are not inferred from later clipboard activity.

The set covers the reported billing reply, its unfenced and wrapped forms,
command handoffs, multiple picks in order, explanatory snippets, already-sent
messages, Unicode, and an oversized block. The original billing draft fits the
old size limit; it tests recognition of an implicit draft rather than truncation.
The longer stress variant separately tests the old 1,000-character cutoff.

`TestJevCopyCorpus` gives the selector oracle labels, then checks exact output and
boundaries. It tests candidate availability and assembly, **not model quality**.
Existing copy-key tests cover the clipboard, caching and retry behavior.

To measure the classifier, explicitly opt into the live service. This sends the
fixture messages to TypeSafe using the existing key; it does not write the
clipboard or send any draft to its intended recipient:

```sh
JEV_COPY_EVAL=/tmp/copy-eval TYPESAFE_API_KEY="$TYPESAFE_API_KEY" \
  env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$(mktemp -d)" \
  go test -short ./internal/ui -run '^TestJevCopyEval$' -v -timeout 4m
```

Alternatively, `JEV_EVAL_CONFIG` names the board configuration directory holding
the saved key. `JEV_COPY_CASES` selects another JSON corpus in the same schema;
keep real conversation corpora and generated reports outside the repository.
`JEV_COPY_FILTER=billing` runs only matching case identifiers.

The run makes at most two requests per case. `results.json` records the model,
limits, threshold, exact per-item output, probabilities, questions, request errors,
and elapsed time. It compares the prior question, 1,000-character candidate limit
and 1,500-character context with the current production selector. A request error
counts as a failed case, never as a correct empty selection. The test fails if
any current case differs from its label, after writing the report.

Use exact-case accuracy and missed/extra item counts together. A partial draft is
both a missed complete reply and an extra incorrect selection. This is a small,
purposefully selected regression set, not a representative or held-out estimate of
all conversations; keep new failures as separate cases when tuning the wording.
