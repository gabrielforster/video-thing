# Task 6: Extend `scripts/e2e.sh` for listing, pagination, and deletion

> Task 6 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 5](task-05-structured-json-logging-x-request-id.md) · Next: [Task 7](task-07-update-docs-invalidated-by-this-plan.md)

---

**Files:**
- Modify: `scripts/e2e.sh`

**Interfaces:**
- Consumes: `$ID` (the video that reaches `ready`, already tracked by the existing script), `$PORT`, `$AWS_ENDPOINT_URL`, `$PROCESSED_BUCKET` (all already exported at the top of the script).
- Produces: exit code 0 only if the created video also appears in `GET /videos`, bad pagination is rejected with `400`, and `DELETE` leaves both the row and the processed objects gone.

This appends to the script, reusing its existing `$ID` (the fully-processed video from the retry loop), its `$TMP` scratch directory, and its `FAIL: ... >&2; exit 1` style. It runs after the existing final `echo "PASS: ..."` block (currently the last 4 lines of the file).

- [ ] **Step 1: Verify the JMESPath expression used below**

This was run against the real LocalStack instance while writing this plan:

```bash
aws --endpoint-url http://localhost:4566 s3api list-objects-v2 \
    --bucket video-thing-dev-processed-assets --prefix "processed/nonexistent-prefix/" \
    --query 'length(Contents || `[]`)' --output text
```
Expected: `0` (the `|| \`[]\`` guards against `Contents` being absent from the response when no keys match).

- [ ] **Step 2: Append the new checks to `scripts/e2e.sh`**

Add this after the script's existing final block (`echo "PASS: video $ID reached ready ..."` / `... which is readable unsigned and cross-origin"`):

```bash

echo "==> checking GET /videos includes the created video"
curl -sf "localhost:$PORT/videos" >"$TMP/list.json"
if ! jq -e --arg id "$ID" '.items | map(.id) | index($id) != null' "$TMP/list.json" >/dev/null; then
    echo "FAIL: video $ID not present in GET /videos items" >&2
    cat "$TMP/list.json" >&2
    exit 1
fi
if [ "$(jq -r '.pagination.limit' "$TMP/list.json")" != "20" ] || [ "$(jq -r '.pagination.offset' "$TMP/list.json")" != "0" ]; then
    echo "FAIL: default pagination is not limit=20/offset=0:" >&2
    jq .pagination "$TMP/list.json" >&2
    exit 1
fi

echo "==> checking pagination bounds are rejected"
for bad_query in "limit=0" "limit=101" "limit=abc" "offset=-1" "offset=abc"; do
    CODE="$(curl -s -o "$TMP/bad-page.json" -w '%{http_code}' "localhost:$PORT/videos?$bad_query")"
    if [ "$CODE" != "400" ] || [ "$(jq -r .error.code "$TMP/bad-page.json")" != "invalid_request" ]; then
        echo "FAIL: GET /videos?$bad_query returned $CODE / $(jq -r .error.code "$TMP/bad-page.json" 2>/dev/null)," >&2
        echo "      want 400 / invalid_request" >&2
        exit 1
    fi
done

echo "==> deleting video $ID and checking cleanup"
CODE="$(curl -s -o /dev/null -w '%{http_code}' -XDELETE "localhost:$PORT/videos/$ID")"
if [ "$CODE" != "204" ]; then
    echo "FAIL: DELETE /videos/$ID returned $CODE, want 204" >&2
    exit 1
fi

CODE="$(curl -s -o /dev/null -w '%{http_code}' "localhost:$PORT/videos/$ID")"
if [ "$CODE" != "404" ]; then
    echo "FAIL: GET /videos/$ID after delete returned $CODE, want 404" >&2
    exit 1
fi

REMAINING="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/" \
    --query 'length(Contents || `[]`)' --output text)"
if [ "$REMAINING" != "0" ]; then
    echo "FAIL: $REMAINING objects remain under processed/$ID/ after DELETE" >&2
    exit 1
fi

echo "PASS: video $ID appears in GET /videos, invalid pagination is rejected with 400,"
echo "      and DELETE removed both the row and every processed object"
```

- [ ] **Step 3: Run it from a cold stack**

```bash
docker compose down -v
make e2e
```
Expected: the script prints all of its existing `PASS`/`==>` lines plus the four new sections above, and exits 0.

- [ ] **Step 4: Commit**

```bash
git add scripts/e2e.sh
git commit -m "test: extend e2e.sh for listing, pagination, and deletion"
```

---
