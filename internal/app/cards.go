package app

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/i41-eu/q41eu-pet/internal/pet"
)

// Food cards (NFC, F1-F6): the parent picks each card's food (Settings.Foods); the robot's
// reads of every card are kept, so the parent page can show which card is which.

// cardRead notes a food card the robot read: when (for the parent page), and an unknown card
// (no food chosen yet) among the new ones. a.mu held.
func (a *App) cardRead(r *robot, uid string, now time.Time) {
	if uid == "" {
		return
	}
	r.cardSeen[uid] = now
	if _, ok := r.pet.Settings.Foods[uid]; !ok {
		r.unknownTags[uid] = now
		trimTags(r.unknownTags)
	}
	r.pruneCards()
	a.dirty = true
	// The parent page asks for the cards again (/api/parent, behind the PIN): the event names
	// no card, as the kid's page gets it too.
	a.publish(r.id, sseEvent{"card", mustJSON(map[string]string{"robot": r.id})})
}

// cardRecent: a card read this recently blinks on the parent page, so the parent holding it
// sees which row it is.
const cardRecent = 5 * time.Minute

// cardView is a food card on the parent page.
type cardView struct {
	UID    string    `json:"uid"`
	Food   string    `json:"food"`             // the parent's choice; "" = none yet (it feeds an apple)
	Seen   time.Time `json:"seen,omitzero"`    // the robot's last read; none = never (or not since reads are kept)
	Recent bool      `json:"recent,omitempty"` // read within cardRecent
}

// cardViews lists the parent's cards and the unknown ones, the most recently read first; cards
// never read come last, by food (in the menu's order), then by uid. a.mu held.
func (a *App) cardViews(r *robot, now time.Time) []cardView {
	cards := []cardView{}
	add := func(uid, food string) {
		seen := r.cardSeen[uid]
		cards = append(cards, cardView{UID: uid, Food: food, Seen: seen, Recent: !seen.IsZero() && now.Sub(seen) < cardRecent})
	}
	for uid, food := range r.pet.Settings.Foods {
		add(uid, food)
	}
	for uid := range r.unknownTags {
		if _, ok := r.pet.Settings.Foods[uid]; !ok {
			add(uid, "")
		}
	}
	slices.SortFunc(cards, func(x, y cardView) int {
		if c := y.Seen.Compare(x.Seen); c != 0 { // never read (the zero time) last
			return c
		}
		if c := cmp.Compare(slices.Index(pet.FoodOrder, x.Food), slices.Index(pet.FoodOrder, y.Food)); c != 0 {
			return c
		}
		return strings.Compare(x.UID, y.UID)
	})
	return cards
}

// pruneCards forgets the reads of cards that are gone: neither one of the parent's
// (Settings.Foods) nor among the unknown ones. a.mu held.
func (r *robot) pruneCards() {
	for uid := range r.cardSeen {
		_, chosen := r.pet.Settings.Foods[uid]
		if _, unknown := r.unknownTags[uid]; !chosen && !unknown {
			delete(r.cardSeen, uid)
		}
	}
}
