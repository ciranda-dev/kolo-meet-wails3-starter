package main

import "fmt"

// Security modes. Only one exists: every call runs through the relay of its host, with a key per sender.
const modeGroupKeysHostRelay = "group-keys-host-relay"

// Security is what the window says about the protection of the call. The sentences come from here so that they
// match what the code does; a window in another language translates by Mode, Operator, Participants, Waiting and
// Complete.
type Security struct {
	Mode string `json:"mode"`
	// Header is the line above the call: how many are in it and how it is protected.
	Header string `json:"header"`
	// Detail is the text shown when the header is tapped.
	Detail string `json:"detail"`
	// Operator is the display name of the person who runs the relay; OperatorIsYou says it is this person.
	Operator      string `json:"operator"`
	OperatorIsYou bool   `json:"operatorIsYou"`
	Participants  int    `json:"participants"`
	// Waiting counts the participants whose keys have not arrived. Nothing of them plays until they do.
	Waiting int `json:"waiting"`
	// Complete is true while every participant's keys have arrived. Only then does the header say "end-to-end
	// encrypted" (decision 0010: the end-to-end rows appear only while every participant's keys arrived).
	Complete bool `json:"complete"`
}

// detailCommon follows decision 0010, "What the meeting example may say", for a relay hosted by a participant, with
// "names" where it says "messages" (this app has no messages). Display names are padded to a fixed size before they
// are sealed, so the relay does not see how long a name is.
const detailCommon = "The relay cannot see audio, video or names, not even how long a name is. " +
	"Anyone in the call could, with the relay's help, make audio, video or names that seem to come from someone else. " +
	"The relay sees who is in the call, who watches whom, when each person sends and how much, " +
	"when microphones and cameras are off, and the room ID; it can drop or delay media. " +
	"Anyone in the call can read everyone's media. There is no protection for the current key period if a device is " +
	"compromised, and no recovery after a compromise."

const detailWaiting = " Nothing from a person who is waiting for keys is played."

// detailConnectionRelays is what the operator of each listed connection relay sees (decision 0013, "Threat model").
const detailConnectionRelays = " Each connection relay in your settings sees your key, your address and when you are in a call, " +
	"and, for the connections it carries, who connects to whom, when and how much. It cannot read them: they are " +
	"encrypted end to end between the two apps."

// securityOf describes a call of n participants, waiting of them without keys, whose relay is run by operator;
// relays is the number of connection relays in the settings.
func securityOf(n, waiting int, operator string, operatorIsYou bool, relays int) Security {
	s := Security{Mode: modeGroupKeysHostRelay, Operator: operator, OperatorIsYou: operatorIsYou, Participants: n, Waiting: waiting, Complete: waiting == 0}
	run := fmt.Sprintf(" %s runs the relay, so %s could make audio, video or names that seem to come from someone else.", operator, operator)
	by := operator
	if operatorIsYou {
		run = " You run the relay, so you could make audio, video or names that seem to come from someone else."
		by = "you"
	}
	extra := ""
	if relays > 0 {
		extra = detailConnectionRelays
	}
	if s.Complete {
		s.Header = fmt.Sprintf("%d in call, end-to-end encrypted, through relay run by %s", n, by)
		s.Detail = detailCommon + run + extra
		return s
	}
	s.Header = fmt.Sprintf("%d in call, %d waiting for keys, through relay run by %s", n, waiting, by)
	s.Detail = detailCommon + run + detailWaiting + extra
	return s
}

// leftSentence is the notice for a participant who left: they no longer get your key, so they read nothing new.
func leftSentence(name string) string { return name + " no longer receives your media." }
