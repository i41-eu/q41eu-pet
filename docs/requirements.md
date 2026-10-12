# q41eu-pet requirements

Everything the pet must do, in one place, so a change can be checked against
the rest before it is made. Each requirement has an id (`N3`, `T5`, ...) to
refer to it in discussions and commits. Numbers in parentheses are the
current values; the code is the authority on them.

When a change touches a requirement, update it here in the same commit. When
two requirements pull against each other, write it down under
[Conflicts and open decisions](#conflicts-and-open-decisions) and decide
there, instead of flipping the behaviour back and forth.

## Principles

- **P1** The kid comes first: pictures over text, big buttons, nothing scary.
  The pet never dies or gets ill; neglected, it is only sad and asks for things.
- **P2** Czech is the default language, English the other. Every spoken line
  exists in both (`internal/app/lines/cs.txt`, `en.txt`; a test checks it).
  The Czech must sound natural Czech (no "Mrrr"); kid-friendly wording
  ("Hlazení mi dodává energii a dobrou náladu", "Tohle mám moc rád").
- **P3** Settings belong to the parent page behind a PIN, never on the kid's page.
- **P4** The robot's firmware sends primary data only (raw sensor values,
  hardware events); the pet decides what they mean.
- **P5** The robot moves its head only as part of a feature the user asked for
  (games, dance, lifting a sunken head); nothing else moves or unpowers it. It
  never moves while the robot is in someone's hands (R9).
- **P6** Server code is Go (standard library plus gorilla/websocket).
- **P7** The pet's own needs pause while the kid sleeps or is at school, so it
  never suffers while the kid can't care for it.

## Needs and mood

- **N1** Three needs from 0 to 100: food (apple), fun (heart), energy (zzz).
  Energy is shown as "zzz", never as a battery, so kids don't confuse it with
  the robot's real battery.
- **N2** While awake, needs drop per hour: food (12), fun (15), energy (7),
  times the difficulty: easy 0.5, normal 1, hard 1.5.
- **N3** Needs pause at night and during school hours (P7).
- **N4** Only sleep gives energy (a night, a nap). Food never does (a test checks it).
- **N5** The mood follows the needs, first match wins: night → sleeping,
  nap → napping, food < 25 → hungry, energy < 20 → tired, fun < 25 → bored,
  all ≥ 60 → happy, else OK.
- **N6** Awake and not at night, the pet nags about a low need (hungry,
  bored, tired) at most every 30 minutes, with a line, emotion and sound.
- **N7** Both pages show the needs live (refreshed every 2 s), e.g. rising during a nap.

## Daily routine

- **D1** Wake-up and bedtime per weekdays and weekends (07:00–20:00, 08:00–20:30);
  optional school hours on weekdays (08:00–15:00).
- **D2** 10 minutes before bedtime the pet yawns and says it is almost bedtime.
- **D3** At bedtime: lullaby, "good night", the screen dims (8%), a warm
  night light that fades to off within 10 minutes; the screen turns off 5
  minutes after it was last lit.
- **D4** At night, a light touch (one finger on the head, a tap on the screen)
  gets a sleepy "Zzz" or, sometimes (40%), a dream picture, silently. A touch
  lights the screen dimmed for another 5 minutes.
- **D5** At night, the whole palm on the head (all three zones at full, 3,3,3)
  wakes the pet quietly for a few minutes (parent setting, 5; 0 = never), then
  it falls asleep again with the lullaby.
- **D6** In the morning it lights the screen, greets and plays a tune.
- **D7** School hours (parent setting, weekdays): the pet rests. The screen
  goes off and the LEDs too; whatever the kid does (head, cards, screen,
  shaking, the kid page) gets only "Během školy já odpočívám." (the screen
  lights for the line, then goes dark again). Nothing is eaten or played. After
  school the face comes back. In holidays the parent turns school off. In demo
  mode school is ignored by default (X5).

## On the robot

### Head touches

- **T1** Kinds of touch, each with its own lines and fun: tickle (short
  < 300 ms and light, +3), cuddle (+6), long cuddle (held ≥ 1.5 s, reacts
  while the hand still rests, +8), scratch (2 swipes within 2.5 s, +10).
  Fun from touches at most every 4 s; the reaction still shows.
- **T2** Asleep (night or nap), one finger shows a dream; the whole palm wakes (D5, S3).
- **T3** A food card held to the robot touches its head too. Such a touch must
  never count as a cuddle: only the card counts. Today: a touch reaction waits
  0.5 s after the release for a card, and touches within 2 s of a card are
  ignored. Measured: the card arrives 120–370 ms after the head
  touch (the firmware polls twice a second; a read takes 230–460 ms), and a
  card with its touch counts as the card only.

### Food cards (NFC)

- **F1** A card is a food (apple, carrot, banana, bread, milk, cake); the parent
  assigns each card's food; an unknown card works as an apple right away.
- **F2** Eating shows the food gliding into the mouth, a food-specific funny
  line ("Mňam", ...), no hearts and no "I love you".
- **F3** Full (food ≥ 90): refuses.
- **F4** Picky: the same food again within 30 minutes. The 2nd time it refuses
  ("Nemáš něco jiného?"); the 3rd and later it eats it with "No dobře..." for
  80% of its value.
- **F5** Asleep, a card is not eaten: the pet dreams of that food. From the 2nd
  card in the same sleep on, each card adds +15% energy (helps it sleep).

### Screen

- **M1** By day a tap opens the menu (robots without sprites: the needs
  picture). A tap while a picture covers the face returns to the face. Asleep,
  a tap is a dream (T2), it does not wake the pet.
- **M2** A long press asks for gentleness: "Jemně a krátce, prosím. Můj
  obličej je citlivý." (or "Moje obrazovka je citlivá."). It closes an open
  menu, and never starts a game or opens a menu.
- **M3** Menus are still pictures (sprites) the robot reports taps on:
  - main: food, play, nap, needs (a 2×2 grid);
  - food: the six foods;
  - play: catch the ball, the color game;
  - needs: the needs picture with all three bars.
- **M4** Every menu screen has the same back arrow at the bottom center, and
  no other close control. Back goes to the menu it came from; on the main menu
  (or a screen opened from outside the menu) it goes to the face.
- **M5** The main menu's needs tile shows all three needs (icons with bars),
  not a single heart.
- **M6** A menu closes by itself after 15 s without a tap.
- **M7** Any tap on the face counts, also one landing on its eyes or mouth (menu
  tiles sit over the eyes). A double tap on a tile or button is two taps; a
  double tap elsewhere blanks the screen (the robot's screensaver). Any reaction
  (e.g. a cuddle) closes an open menu; a game start too.

### Other robot input

- **O1** Shaking: "Wheee", dizzy, a little fun (+2).
- **O2** Someone comes close (proximity): a hello at most every 20 minutes.

### What the robot shows and says

- **R1** Face: Fluent Emoji faces per mood filling the whole 4:3 screen (the
  default), or (parent setting) the robot's own face with the mood's emotion; the
  LEDs show the mood's colour either way. Drawn faces step aside for speech bubbles
  and full-screen pictures.
- **R2** Speech: Edge "Antonín" voice, a bit higher and for kids, lightly
  robotized (`antonin-2-kid-robot-light`); English: Ana. Lines are made once
  and cached; without the network, espeak-ng. Not too loud (no clipping on the
  robot's speaker). The speech bubble shows lines without diacritics (the
  robot's font has none).
- **R3** One line at a time: a new line cuts the old one off softly; no line twice.
- **R4** Sounds are under 3 s; speech and sound effects mix on the robot
  without noise.
- **R5** Pictures and sounds the pet uses are kept in the robot's file store
  (`pet/`, about 1.9 MB), uploaded when missing or changed (CRC-32), stale ones deleted.
- **R6** The firmware lets the head servos go at rest and the head sinks. By
  day the pet lifts it back to where it last put it (rest pitch 25°, when
  8° lower, at most every 20 s); a head turned more than 30° goes back to the
  middle. At night and during naps it may droop.
- **R7** By day the screen stays on with the pet's face; the robot must not
  look asleep while the pet is awake (the robot blanks its screen after 60 s
  without commands, so the pet sends its face again every 30 s when idle). A
  screen blanked on purpose (a double tap, the night) stays dark, and so does
  the screen during school hours (D7).
- **R8** While the pet sleeps (a nap, or the screen lit at night), a tiny faint
  bar in the bottom left corner shows its energy in % of full (10% steps,
  colored like the needs, no icon), rising as it rests.
- **R9** The head never moves while the robot is in someone's hands: held is a
  tilt of more than 12° from the learned rest pose, turning (gyro over 8°/s),
  more than 0.15 g off 1 g, or a shake; free again after two calm samples at
  rest (telemetry every 2 s). Every head command (look, nod, shake, home) is
  dropped while held.

## Play

- **G1** Catch the ball: from the kid page's play button or Play → ball in the
  robot's menu. Five balls, a few seconds each (parent: 3–10, 5); the kid taps
  the ball on the robot's screen; a wrong tap is ignored; a missed ball moves on.
- **G2** The head makes later balls harder (parent can turn it off): ball 1
  still, ball 2 circles, ball 3 random moves, balls 4–5 dodge a hand coming
  close (proximity 20 above its normal level, or the hand's shadow: light below
  40% of normal; 20 samples/s; each dodge adds a second).
- **G3** At the end: one star per catch and the score spoken in words
  ("Paráda, pět z pěti"). More catches, more fun.
- **G4** Play costs energy and food (fun +20, energy −8, food −4); too tired
  (energy < 15) it refuses. Daily play limit (parent, minutes; feeding is never limited).
- **G5** Without the robot connected, play is a short dance on the page.
- **G6** The color game (robot menu: Play → the color-dots tile): six color
  buttons (red, yellow, green, white, blue, purple; not cyan, which looked like
  blue on the LEDs), three by two, in a new order every level. The robot's LED
  strips show colors; the kid presses them in the level's order:
  1. the left strip, then the right one;
  2. the right strip, then the left one;
  3. left near the screen, left far, right near, right far;
  4. right far, right near, left far, left near;
  5. the four halves in a random order, not level 3's or 4's.

  Each level starts with a picture of the robot from above (its screen towards
  the kid) with its strips numbered in that order (6 s), then 5 rounds: new
  colors, the buttons shuffled (25 rounds in a game). Colors are random per
  part; the same color twice in a row is a double tap. A pressed part goes dark.
  A wrong button buzzes and greys all buttons out for 3 s, they take no presses
  meanwhile (a penalty; the clock runs on); a round not done in 20 s
  moves on and counts 20 s. At the end the robot says the time over all rounds
  ("Hotovo za 74 sekund!"); a game with every round done can get on the
  leaderboard (G8; the fastest says "Nový rekord!"). Same fun, energy and food
  as catch (G4), by the share of rounds done. A double tap on a button
  is two presses, never the screensaver. The LED colors match the buttons
  (gamma corrected, green weakened, the same power; yellow, purple and white
  tuned by eye).
- **G7** During a game the screen has a fixed brightness (70%): the kid's hand
  near the light sensor must not dim it. Auto brightness returns after the game.
- **G8** The color game's leaderboard: the three best full games (every round
  done), kept on the server (the state file, photos next to it), so a robot
  restart loses nothing. A game fast enough for it: the robot says the place
  ("Jsi na prvním místě! Usměj se, vyfotím tě."), counts down ("Tři, dva,
  jedna, sýr!") and takes a photo with its camera (parent setting, on by
  default). After every color game the robot shows the podium (first in the
  middle, photos in gold, silver and bronze frames, the times); the kid's page
  shows it too. The parent page lists it, removes a place or clears it (the
  photos go too). Photos are served only to browsers paired with the robot.

## Naps

- **S1** A nap lasts 15 minutes unless woken. Napping is fine anywhere from 10
  to 90% energy; at 90% or more the pet is not tired (the same in demo mode).
- **S2** The kid page has a nap button; the robot's menu has a nap tile.
- **S3** The whole palm on the head wakes a napping pet; a tap or one finger does not (dream).
- **S4** A nap that ends wakes the pet by itself (face, line), once.

## Demo mode (parent setting)

For showing the pet: something always happens within a minute.

- **X1** A need reaching 90% drops back to 10%, with a reset picture (arrows,
  the need's icon, its bar at 10%) and a line that names the demo mode
  explicitly ("Ukázkový režim: a zase mám hlad!"), no "magic".
- **X2** A nap is quick: +20% energy within 5 s, then towards 75% at 1 minute,
  then the pet wakes by itself (auto-wake at about 75%).
- **X3** The next nap passes 90% within seconds; the demo reset then ends the
  nap (back to 10%).
- **X4** Dream cards (F5) work the same; past 90% they trigger the reset too.
- **X5** Demo mode ignores school hours (a parent checkbox, on by default), so
  a demo works on a school morning.

## Kid's page

- **K1** Opened by scanning the robot's QR code (pairs the browser with the robot).
- **K2** The tiny colourful Stackchan face (mood), three need bars, four big
  picture buttons: food, cuddle, play, nap. Robot actions show on the page too.
  The color game's podium (G8) when there is one, not during games or at night.
- **K3** No settings, no text the kid must read.

## Parent page

- **A1** `/parent`, behind a PIN of 4–8 digits; the first PIN entered becomes
  the PIN. 5 wrong tries: wait a minute.
- **A2** Unlocked for 30 minutes after the PIN, except on a parent's device:
  marked so at the PIN prompt or on the page, it stays unlocked (also across
  restarts) until the Lock button; then the PIN unlocks it for good again.
- **A3** Settings: name, language, difficulty; schedule (D1) and school; daily
  play limit; sounds, voice, volume (40); night light, screen off at night,
  night wake minutes; drawn faces; demo mode; game ball seconds and head moves;
  photos for the color game's leaderboard; food per card.
- **A4** Set the needs directly (sliders, all full).
- **A5** A log of what happened; previews of morning, bedtime, the needs
  picture, every sound and spoken line on the robot.
- **A6** New pet (needs and age reset, settings kept), change the PIN.
- **A7** Demo mode has its own section at the bottom of the page (on/off,
  ignore school); its checkboxes save at once.
- **A8** The color game's leaderboard with photos: remove a place, or clear it
  (the photos go too).

## Reliability

- **W1** The pet's state (pets, PINs, pairings, parent devices, the color
  game's leaderboard and its photos) is kept on the server and survives
  restarts of the server (saved every minute and on exit) and of the robot.
- **W2** A robot that reconnects gets its face and files back in step.
- **W3** A watchdog (every 2 s) repairs what the engine would otherwise only
  fix on the next change, and logs each repair: a menu left open, a game that
  stopped, a picture left on, a busy state longer than 2 minutes, dropped
  commands, the screen (R7).
- **W4** Demo resets (X1) and nap ends (S4) are checked every 2 s, not only every 15 s.

## Development

- **V1** An offline host simulator for the robot's drawing code
  ([`firmware/tests`](https://github.com/i41-eu/i41eu-embody/tree/main/firmware/tests)), and screen snapshots from the real robot
  (`POST /api/debug/{id}/run`), to check pictures without guessing.
- **V2** Test on the LAN server, not on the public one.
- **V3** Before committing: `gofmt -l .`, `go vet ./...`, `go test -race ./...`.

## Conflicts and open decisions

- **C1 Nap refused at ≥ 80% vs. the demo cycle (S1, X2, X3).** Decided:
  naps are allowed from 10 to 90% in every mode (S1).
- **C2 The screen during school hours (R7).** Decided: off; the pet
  rests and says only that it rests (D7).
- **C3 Faster card reads (T3).** Polling four times a second with a wake on
  head touch read no cards at all (firmware be75bc2, reverted). Twice a second
  is fast enough for now; revisit only if cards feel slow.
- **C4 Game dodging (G2).** Proximity mostly stayed at 0–30 in games (the rise
  threshold was 60) and the shadow rule (60%) fired 5 times in one game.
  Decided: rise 20, shadow below 40% (G2); check in the next games.
- **C5 Long press (M2).** Decided: a line asking for gentleness.
- **C6 Not yet seen on the robot:** the held detection (R9; its
  thresholds may need tuning), the leaderboard's countdown, photo and podium
  (G8; the camera snapshot itself works), dodges with the new thresholds (C4).
- **C7 The level picture (G6)** shows the robot from above; whether small kids
  read it that way is still to be seen.
