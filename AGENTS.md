# q41eu-pet

Tamagotchi server for Stackchan Embody Mode. See [readme.md](readme.md).

- **[docs/requirements.md](docs/requirements.md) is the pet's specification**: every requirement with an id, and the open decisions. Check a change against it, and update it in the same commit; record conflicts there instead of flipping behaviour back and forth.
- Go only: the standard library plus gorilla/websocket (and pgx, only through q41eu-raw's `statestore`). The protocol comes from `github.com/i41-eu/q41eu-raw/wire` (the version pinned in `go.mod`); change it there, and use an uncommitted `go.work` to try both together.
- The kid's page is for small kids: pictures over text, big buttons, nothing scary (the pet never dies). Settings belong on the parent page behind the PIN.
- The pet's spoken lines live in `internal/app/lines/cs.txt` and `en.txt` (sections per situation, one variant per line); keep both languages complete (`TestLinesFilesAreComplete`). The pages' own texts are the `T` tables in `ui/index.html` and `ui/parent.html`. The speech bubble folds Czech letters (the robot font has none).
- Keep sounds under 3 s (the robot's speaker buffer); `internal/sound` tests check it.
- Before committing, run `gofmt -l .`, `go vet ./...` and `go test -race ./...`.
- See the robot's screen while testing: `POST /api/debug/{id}/run` with `shots_ms` (and an `action`, e.g. `menu`, `colors`, `nap`); the JPEGs land in `~/.cache/stackchan-pet/screens/`. Don't open camera snapshots (people at home).
