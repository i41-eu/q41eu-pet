# q41eu-pet

<img src="docs/img/robot3d-pet.png" width="240" align="right" alt="A Stackchan robot running the pet: a happy face, its LEDs warm">

A Tamagotchi for [Stackchan](https://shop.m5stack.com/products/stackchan-kawaii-co-created-open-source-ai-desktop-robot) robots
running Embody Mode. The pet lives on this server; the robot is its body.
Kids play with the robot itself and with a picture page on a phone or tablet.
Parents set the daily routine on a page of their own: signed in as the robot's owner
(pet.q41.eu), or behind a PIN (a pet at home).

It is a separate server that the robot switches to on its app switcher (the QR screen:
Next, then Connect), or from its manager's page. It speaks the Embody Mode protocol from
[q41eu-raw](https://github.com/i41-eu/q41eu-raw) (`wire` package).
The robot runs Embody Mode from the
[i41eu-StackChan firmware](https://github.com/i41-eu/i41eu-StackChan); setting it up:
[SETUP.md](https://github.com/i41-eu/i41eu-StackChan/blob/embody/firmware/main/apps/app_embody_mode/SETUP.md).
It runs at [pet.q41.eu](https://pet.q41.eu): set your robot up on
[m.i41.eu](https://m.i41.eu) with the pet among its apps, or run the pet on a server at home.

| The robot's face, per mood | The kid's page |
|---|---|
| ![The pet's faces on the robot's screen: happy, yum, fine, yawning, asleep, hungry, bored](docs/img/faces.gif) | <img src="docs/img/kid-page.png" alt="The kid's page: the pet, its three needs and four big buttons" width="260"> |
| ![All seven faces](docs/img/faces.jpg) | |

The faces are [Fluent Emoji](https://github.com/microsoft/fluentui-emoji) (MIT) made to fill the
robot's 4:3 screen; a parent can switch back to the robot's own face.

Part of [i41eu-arch](https://github.com/i41-eu/i41eu-arch), a local first, privacy
first platform for a home, as a separate app a home includes when its family wants it.

All requirements, with ids and the open decisions, are in
[docs/requirements.md](docs/requirements.md); this readme is the overview.

> **A proof of concept, vibe coded.** Written with AI agents and tested on real hardware at
> home, but neither the code nor its security has been reviewed by humans. Use it on your
> own network, and don't trust it with anything private yet.
>
> **Early stage: no backward compatibility.** Protocols, APIs, file formats and stored settings
> change when something better comes along, without migrations: update the robot's firmware
> and the servers together.
>
> **Want more?** Ask in the [issues](https://github.com/i41-eu/q41eu-pet/issues), and ideally [sponsor mj41](https://github.com/sponsors/mj41) on GitHub:
> mj41 codes for attention food.

## How the pieces fit

| Piece | What it is | Needed? |
|---|---|---|
| **Robot** | a Stackchan with the [Embody Mode firmware](https://github.com/i41-eu/i41eu-StackChan/tree/embody/firmware/main/apps/app_embody_mode). On its screen the **app switcher** (the QR screen: Next, Connect) lists its apps and switches between them; its gear opens the **Manager screen** (which manager it has, turn it off or on). | yes |
| **Apps** | what the robot does, one at a time. Each app is a web service: the robot connects to it, and a phone or browser opens its web page. This repo is [Pet](https://s.i41.eu/pet); the apps online: [s.i41.eu](https://s.i41.eu/#apps) | at least one |
| **Phone or browser** | opens an app's page and pairs with the robot by scanning its QR code; with end-to-end encryption only paired browsers can read the robot | to use an app |
| **Manager** | [i41eu-manager](https://github.com/i41-eu/i41eu-manager): the web service that sets robots up over USB, gives each robot its own token per app, and switches and changes apps from its page; on your own computer (the home manager) or online at [m.i41.eu](https://m.i41.eu), which a home manager may link to | optional: without it, apps are written over USB and switched on the robot's app switcher |

"Manager" always means this web service; on the robot there is only the app switcher and the
Manager screen that shows which manager it has.

## Playing

| On the robot | The pet |
|---|---|
| Stroke or press the head | Tickle, cuddle, long cuddle or scratch: each its own line and fun |
| Hold an NFC card to it | Eats the card's food (it glides into the mouth); picky about the same food twice; asleep, it dreams of the food, and more cards help it sleep (+15% energy each) |
| Tap the screen | A menu: food, play (catch the ball, the color game), nap, needs; a back arrow at the bottom |
| Press the screen long | "Jemně a krátce, prosím. Můj obličej je citlivý." ("Gently and briefly, please. My face is sensitive.") |
| Shake it | "Wheee!", dizzy |
| Come close (proximity) | Says hello now and then |

The kid's page (`http://<server>:8770/` after scanning the robot's QR code)
works with pictures, so it needs no reading: the pet's face, three need bars
(food, fun, energy), four big buttons (food, cuddle, play, nap) and the color
game's podium. Actions from the robot show on the page too.

**Catch the ball:** five balls on the robot's screen for the kid to tap; the
head makes each one harder, up to dodging a hand that comes close
([G1–G3](docs/requirements.md#play)). **The color game:** the LED strips show colors, the kid
presses them in each level's order on the screen; the three best games make a
leaderboard with photos from the robot's camera ([G6–G8](docs/requirements.md#play)).

The head never moves while the robot is in someone's hands (the accelerometer
and gyro in its telemetry tell). It sinks at rest (the firmware lets the servos
go), and by day the pet lifts it back
([R6, R9](docs/requirements.md#what-the-robot-shows-and-says)).

Needs: food, fun and energy go from 0 to 100 and drop while the pet is awake;
its mood follows them ([N1–N6](docs/requirements.md#needs-and-mood)). The pet is kind: it never
dies or gets ill; neglected it only gets sad and asks for food or play now and
then.

## Parent page

`/parent` is for the robot's owner. With sign-in through a Stackchan manager
(`-manager-sign-in`, as on pet.q41.eu) the owner signs in (one sign-in for all the manager's
apps; package [sso](https://github.com/i41-eu/q41eu-raw/tree/main/sso)): the manager says whose
robot it is, the owner's signed-in browsers get it without the QR code, and nobody else gets
the parent page; robots with the shared token belong to `-admin-emails`. The parent page links to
the manager (the robot and its apps). Kids pair by the QR
code without an account. Without sign-in (a pet at home) `/parent` asks for a PIN; the first PIN
entered becomes the PIN. A parent's phone can be marked as such: it then stays unlocked until
its Lock button.
Settings ([A1–A8](docs/requirements.md#parent-page)):

- name, language (Czech or English), difficulty (how fast needs drop)
- wake-up and bedtime for weekdays and weekends; optional school hours, when
  the pet rests with its screen off ("Během školy já odpočívám.", "During school I rest.")
- minutes of play per day (feeding is never limited)
- sounds, voice and volume, night light, screen off at night, night wake minutes; by day the
  screen goes off when nobody plays (5 minutes by default, 0 = never) and a touch or play from
  a phone lights it again
- food cards: tags seen by the robot, each assigned a food
- the needs themselves: sliders for food, fun and energy, or all full
- the games: seconds per ball, whether the head moves; photos for the color
  game's leaderboard, which the page lists (remove a place, clear it)
- faces on the robot (on by default): Fluent Emoji faces filling the whole screen, one per
  mood (`internal/robotpic/faces/`, made by `genfaces`); off: the robot's own blinking face
- a log of what happened, previews of the morning and bedtime routines and of every sound
- demo mode, in its own section at the bottom: a need at 90% drops back to
  10%, naps are quick (+20% in 5 s, 75% at a minute, then it wakes), and school
  hours are ignored

The daily routine: the pet yawns before bedtime, then a lullaby, good night
and a warm night light that fades out; at night a light touch gets a sleepy
"Zzz..." or a dream, and in the morning it greets and plays a tune
([D1–D7](docs/requirements.md#daily-routine)). Needs pause at night and during school hours, so
the pet never suffers while the kid sleeps or is away.

## Running

```sh
go run ./cmd/q41eu-pet -tz Europe/Prague
```

| Flag | Default | |
|---|---|---|
| `-listen` | `:8770` | robots and browsers |
| `-public-url` | `http://<LAN IP>:8770` | base of the QR link |
| `-token-file` | `~/.config/stackchan-server/robot-token` | robot bearer token, generated if missing |
| `-state-file` | `~/.local/state/stackchan-pet/state.json` | pets, PINs, pairings, the leaderboard; its photos go to `photos/` next to it; `""` = memory only (no photos) |
| `-state-database` | `$STATE_DATABASE_URL` | Postgres instead (`postgres://user@host/db`, the password from `PGPASSWORD`): the same document and the photos, with the last 50 saves (q41eu-raw's `statestore`); a `-state-file` that exists, and its photos, are imported once into an empty database; it holds a lock while running and stops if the connection is lost |
| `-tz` | this machine's | the family's time zone for the schedule |
| `-voice-dir` | `~/.cache/stackchan-pet/voice` | where the spoken lines are kept |
| `-ffmpeg` | `ffmpeg` from `PATH` | for the Edge voice; `""` = no Edge voice |
| `-espeak` | `espeak-ng` from `PATH` | the fallback voice; `""` = none |
| `-no-voice` | off | the pet does not speak |
| `-debug-dir` | `~/.cache/stackchan-pet/screens` | screen snapshots from `POST /api/debug/{id}/run` |
| `-manager-url` | | the Stackchan manager that set robots up with a token of their own for this app, e.g. `https://m.i41.eu`; the pet checks those tokens with it |
| `-manager-secret-file` | | this app's secret at the manager |
| `-manager-sign-in` | off | sign in through the manager: the parent page is the robot owner's, no PIN |
| `-admin-emails` | | with `-manager-sign-in`: verified e-mails, the parents of robots with the shared token |
| `-debug` | off | debug logging |
| `-ui-dir` | | development: serve the pages from disk (`internal/app/ui`) |

The default token file is [q41eu-raw](https://github.com/i41-eu/q41eu-raw)'s, so both
accept the same shared robot token.

A robot's apps come from its [Stackchan manager](https://github.com/i41-eu/i41eu-manager). At
home, add the pet to the home manager's catalog, `~/.config/i41eu-manager/apps.json`
([The app catalog](https://github.com/i41-eu/i41eu-manager#the-app-catalog)), with a secret in a
file of its own (e.g. `openssl rand -hex 32 > ~/.config/i41eu-manager/pet-secret`):

```json
{"id": "pet", "name": "Pet", "url": "ws://192.168.1.10:8770", "secret_file": "/home/me/.config/i41eu-manager/pet-secret"}
```

and run the pet with it:

```sh
q41eu-pet -manager-url http://127.0.0.1:8790 -manager-secret-file ~/.config/i41eu-manager/pet-secret
```

Robots set up by the manager then get the pet, with a token of their own for it, which the pet
checks with the manager (the package
[robotauth](https://github.com/i41-eu/q41eu-raw/tree/main/robotauth)); the debug API
(`POST /api/debug/{id}/run`) takes only the shared token. A `v*` tag builds the image
`ghcr.io/i41-eu/q41eu-pet:<tag>` (Alpine with ffmpeg and espeak-ng for the voice).

Switching the robot to it:
[SETUP.md, More servers and apps](https://github.com/i41-eu/i41eu-StackChan/blob/embody/firmware/main/apps/app_embody_mode/SETUP.md#more-servers-and-apps).
Then scan the pet's QR code with the kid's phone.

## Layout

- `internal/pet`: the model (needs, schedule, moods, actions), no I/O
- `internal/app`: robot connections, the engine (robot events → pet actions,
  moods → face, speech, LEDs, sounds), the robot's menus (`menu.go`), the games
  (`game.go`, `colorgame.go`), the leaderboard photos (`photos.go`), the
  watchdog that keeps the robot in step (`watchdog.go`), holding detection
  (`held.go`), the debug endpoint (`debug.go`), HTTP API, pages in `ui/`
- `internal/voice`: the Edge read-aloud client, the ffmpeg robot filter, the cache
- `internal/sound`: synthesized sounds, 16 kHz PCM, each under the robot's 3 s buffer
- `internal/robotpic`: pictures for the robot's screen (needs, food, menu tiles,
  game buttons, the level and podium pictures) and the LED colors

The pet speaks its lines: Microsoft Edge's read-aloud voice (Czech: Antonín, a
little higher and faster, English: Ana), made a bit robot by ffmpeg (40% of a
robotized copy mixed in). Each line is made once and kept in `-voice-dir`; at
start the pet makes all its fixed lines ahead of time. Without Edge (offline) it
falls back to espeak-ng. The speech is streamed to the robot at speaking pace.
The robot's speech bubble font has no Czech letters: the bubble shows the
lines without diacritics.

The pet on the pages is a tiny Stackchan (`internal/app/ui/chan/`, drawn for
this project after the robot) showing the same Fluent Emoji faces as the robot
(see its `NOTICE`). Stackchan is developed and
published by meganetaaan, https://github.com/meganetaaan/stack-chan; the
character is used under its
[derivative work guideline](https://github.com/rt-net/stack-chan/blob/main/GUIDELINE.md),
which asks for that credit where users see it (the pages show it).

Other pictures are [Fluent Emoji Flat](https://github.com/microsoft/fluentui-emoji)
(MIT, `internal/app/ui/emoji/LICENSE`); `internal/robotpic/render-icons.sh`
renders the PNGs for the robot's screen, and the robot's faces: `genfaces` takes the mood
emoji, drops the round head and fills the 4:3 screen with its colour.

The pet keeps its pictures in the robot's file store (folder `pet/`): at
connect it asks for the robot's file list and uploads what is missing or
changed (compared by CRC-32), so later it can show them without sending them
again.

## Development

```sh
gofmt -l . && go vet ./... && go test -race ./...
```

The protocol comes from `github.com/i41-eu/q41eu-raw/wire`. To work on both at
once, clone it next to this repo and add an uncommitted `go.work`
(`go work init . ../q41eu-raw`).

## Related projects

- [i41eu-StackChan](https://github.com/i41-eu/i41eu-StackChan): the robot's firmware, Embody Mode.
- [q41eu-raw](https://github.com/i41-eu/q41eu-raw): the `wire` package, and the full dashboard.
- [q41eu-sbot](https://github.com/i41-eu/q41eu-sbot): another app server on the same protocol (a cockpit for the robot and a TPBot car).
- [i41eu-arch](https://github.com/i41-eu/i41eu-arch): the platform this is part of. All the repos: [The repos today](https://github.com/i41-eu/i41eu-arch#the-repos-today).

Stack-chan (スタックチャン) was created by Shinya Ishikawa and the Stack-chan community ([github.com/stack-chan/stack-chan](https://github.com/stack-chan/stack-chan)); this project is independent of it and of M5Stack.

## License

MIT, see [LICENSE](LICENSE).
