package app

import "time"

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
