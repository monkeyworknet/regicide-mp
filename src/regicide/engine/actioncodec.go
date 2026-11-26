package engine

import (
	"encoding/json"
	"errors"
)

// wireAction is the JSON shape every client (and every replay file) uses. It lives
// here rather than in the server so a replay can be decoded without the HTTP layer.
type wireAction struct {
	Type    string   `json:"type"`
	CardIDs []CardID `json:"cardIds"`
	CardID  CardID   `json:"cardId,omitempty"`
	Seat    int      `json:"seat"`
}

// MarshalAction encodes an action as the wire JSON. A replay file stores actions in
// this form, so a recorded game is readable without Go.
func MarshalAction(a Action) (json.RawMessage, error) {
	switch act := a.(type) {
	case Play:
		return json.Marshal(wireAction{Type: act.actionName(), CardIDs: act.CardIDs})
	case FlipJester:
		return json.Marshal(wireAction{Type: act.actionName()})
	case Yield:
		return json.Marshal(wireAction{Type: act.actionName()})
	case PayDamage:
		// Always the multi-card form: an old single-card action decodes fine, but new
		// recordings should not carry a form we are trying to retire.
		return json.Marshal(wireAction{Type: act.actionName(), CardIDs: act.IDs()})
	case EndPayment:
		return json.Marshal(wireAction{Type: act.actionName()})
	case ChooseNextSeat:
		return json.Marshal(wireAction{Type: act.actionName(), Seat: act.Seat})
	}
	return nil, errors.New("cannot encode unknown action")
}

// UnmarshalAction decodes the wire JSON into an engine action. It validates the
// shape only; the rules are checked when the action is applied.
func UnmarshalAction(raw json.RawMessage) (Action, error) {
	var wa wireAction
	if err := json.Unmarshal(raw, &wa); err != nil {
		return nil, errors.New("action must be an object with a type")
	}
	switch wa.Type {
	case "play":
		if len(wa.CardIDs) == 0 {
			return nil, errors.New("play needs cardIds")
		}
		return Play{CardIDs: wa.CardIDs}, nil
	case "flip_jester":
		return FlipJester{}, nil
	case "yield":
		return Yield{}, nil
	case "pay_damage":
		if len(wa.CardIDs) == 0 && wa.CardID == "" {
			return nil, errors.New("pay_damage needs cardIds")
		}
		return PayDamage{CardIDs: wa.CardIDs, CardID: wa.CardID}, nil
	case "end_payment":
		return EndPayment{}, nil
	case "choose_next_seat":
		return ChooseNextSeat{Seat: wa.Seat}, nil
	}
	return nil, errors.New("unknown action type")
}
