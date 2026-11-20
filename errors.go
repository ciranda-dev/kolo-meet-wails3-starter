package main

import (
	"context"
	"errors"
	"io/fs"
	"runtime"
	"strings"
	"syscall"

	invite "github.com/ciranda-dev/kolo-invite"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// Sentinels of the app, for errors.Is. Every error a window sees is a *MeetError that wraps one of them or an error
// of a Kolo library.
var (
	ErrInvalidInvite = errors.New("kolo-meet: not a call invite")
	ErrBusy          = errors.New("kolo-meet: busy")
	ErrNotFound      = errors.New("kolo-meet: host not found")
	ErrBadGrant      = errors.New("kolo-meet: unusable answer from the host")
	ErrNotReady      = errors.New("kolo-meet: network not ready")
	ErrCallEnded     = errors.New("kolo-meet: call ended")
	ErrNoCall        = errors.New("kolo-meet: not in a call")
	ErrBadName       = errors.New("kolo-meet: bad display name")
	ErrUnknownDevice = errors.New("kolo-meet: unknown device")
	ErrClosed        = errors.New("kolo-meet: closed")
	ErrNotHost       = errors.New("kolo-meet: not the host")
)

// Kinds of MeetError: broad groups, as the window sees them.
const (
	kindInvalidInvite = "invalid-invite"
	kindNotFound      = "not-found"
	kindRefused       = "refused"
	kindExpired       = "expired"
	kindBusy          = "busy"
	kindNetwork       = "network"
	kindPermission    = "permission"
	kindDevice        = "device"
	kindMedia         = "media"
	kindName          = "name"
	kindConfig        = "config"
	kindEnded         = "ended"
	kindCancelled     = "cancelled"
	kindInternal      = "internal"
)

// reasons gives every error sentence a stable ID, its kind and its English text. {detail} is a name, a number or a
// setting, and {cause} the plain words of the underlying error. A window in another language translates by reason
// and fills in the same two values; the English Message is the fallback.
var reasons = map[string]struct{ kind, text string }{
	"cancelled":                    {kindCancelled, "Cancelled."},
	"invalid-invite":               {kindInvalidInvite, "This is not a call invite. Copy the whole invite, it starts with koloinv0."},
	"host-not-found":               {kindNotFound, "Could not find the person who invited you. They must be online with Kolo Meet open and on the same network (public, your own, or this computer)."},
	"host-lost":                    {kindNotFound, "The connection to the host ended before it answered. Try again: asking twice is safe."},
	"join-denied":                  {kindRefused, "The call was full when you asked, or the host did not let you in. Ask the host for a new invite."},
	"invite-expired":               {kindExpired, "This invite has expired. Ask for a new one."},
	"invite-used-up":               {kindExpired, "This invite has no uses left. Ask for a new one."},
	"invite-revoked":               {kindExpired, "The host ended this call or revoked the invite. Ask for a new one."},
	"invite-unknown":               {kindInvalidInvite, "The host does not recognize this invite. Check that you pasted the whole invite."},
	"join-rate-limited":            {kindBusy, "Too many attempts. Wait a minute and try again."},
	"host-busy":                    {kindBusy, "The host is busy. Try again in a moment."},
	"bad-grant":                    {kindRefused, "The host sent an answer this version of Kolo Meet cannot use. Ask for a new invite."},
	"relay-unreachable":            {kindNetwork, "Could not connect to the call's relay: {cause}."},
	"call-unavailable":             {kindNetwork, "Could not start the call: {cause}."},
	"too-many-invites":             {kindBusy, "This call has {detail} invites already. Wait for one to expire or end the call."},
	"call-still-ending":            {kindBusy, "The call is still ending. Try again in a moment."},
	"call-full":                    {kindRefused, "The call is full, or the host's app closed your connection while you were joining. Ask the host."},
	"microphone-silent":            {kindDevice, "The microphone sends only silence. Check that Kolo Meet may use it in your system's privacy settings and that it is not muted."},
	"busy-call":                    {kindBusy, "You are in a call already. Leave it first."},
	"busy-reconnect":               {kindBusy, "Leave the call first."},
	"busy-restarting":              {kindBusy, "The network is restarting. Try again in a moment."},
	"app-closed":                   {kindBusy, "Kolo Meet is closing. Start it again to make or join a call."},
	"no-call":                      {kindBusy, "You are not in a call."},
	"call-ended-host-left":         {kindEnded, "The call ended: the host left or lost the connection."},
	"call-ended-removed":           {kindEnded, "The host removed you from this call."},
	"rejoin-refused":               {kindRefused, "You left this call or were removed from it, so this invite no longer lets you in. Ask the host for a new invite."},
	"not-host":                     {kindRefused, "Only the host can remove someone from the call."},
	"remove-self":                  {kindRefused, "You cannot remove yourself. End the call to leave it."},
	"participant-unknown":          {kindNotFound, "That person is not in the call."},
	"call-ended-lost":              {kindEnded, "The call ended: the connection was lost."},
	"call-ended-closed":            {kindEnded, "The call ended because Kolo Meet closed."},
	"network-not-ready":            {kindNetwork, "The network is not ready yet. Wait a moment, or press Try again if Kolo Meet shows an error."},
	"public-unreachable":           {kindNetwork, "Could not reach the public Kolo network: {cause}. Check your internet connection and press Try again, or choose another network: start Kolo Meet with -bootstrap and the entry nodes of your own network, or with -local to use this computer only."},
	"local-port-taken":             {kindNetwork, "Another program uses {detail} on this computer, so the local network cannot start. Close that program and press Try again, or start Kolo Meet without -local to use the public network, or with -bootstrap and the entry nodes of your own network."},
	"first-node-failed":            {kindNetwork, "Could not start the first entry node on {detail}: {cause}."},
	"first-node-in-use":            {kindNetwork, "Another window, probably Kolo Meet, already runs the entry node on {detail}. Start this window with -bootstrap {detail} instead of -first."},
	"bootstrap-failed":             {kindNetwork, "Could not reach the entry nodes ({detail}): {cause}. Check -bootstrap or KOLO_BOOTSTRAP, or start Kolo Meet without it to use the public network."},
	"network-open":                 {kindNetwork, "Could not open the network: {cause}."},
	"lookup-failed":                {kindNetwork, "Could not look for the host: {cause}."},
	"camera-denied":                {kindPermission, "Kolo Meet may not use the camera. Allow it in your system's privacy settings, then try again."},
	"microphone-restricted":        {kindPermission, "A policy of this computer, such as parental controls or device management, does not let Kolo Meet use the microphone."},
	"microphone-denied":            {kindPermission, "Kolo Meet may not use the microphone. Allow it in your system's privacy settings, then try again."},
	"camera-none":                  {kindDevice, "No camera was found."},
	"microphone-none":              {kindDevice, "No microphone was found."},
	"speaker-none":                 {kindDevice, "No speaker was found."},
	"camera-lost":                  {kindDevice, "The camera stopped working or was unplugged."},
	"microphone-lost":              {kindDevice, "The microphone stopped working or was unplugged."},
	"speaker-lost":                 {kindDevice, "The speaker stopped working or was unplugged."},
	"camera-failed":                {kindDevice, "Could not use the camera: {cause}."},
	"microphone-failed":            {kindDevice, "Could not use the microphone: {cause}."},
	"speaker-failed":               {kindDevice, "Could not use the speaker: {cause}."},
	"device-unknown":               {kindDevice, "That device is not in the list. Refresh the devices and choose again."},
	"codec-unavailable":            {kindMedia, "Video or audio coding is not available on this computer: {cause}."},
	"name-empty":                   {kindName, "Write a name other people will see in the call."},
	"name-too-long":                {kindName, "The name is too long. Use at most {detail} characters."},
	"config-setting":               {kindConfig, "The setting -{detail} {cause}."},
	"data-dir":                     {kindConfig, "Could not open the data directory: {cause}."},
	"data-dir-open":                {kindConfig, "The data directory {detail} is open to other users. Make it private (chmod 700) or choose another one."},
	"connection-relay-not-allowed": {kindNetwork, "The connection relay {detail} does not let Kolo Meet hold a place there. Ask its operator to allow your key (the kolo-id line in the terminal), or start Kolo Meet without it."},
	"connection-relay-full":        {kindNetwork, "The connection relay {detail} is full. Kolo Meet tries again; your other connection relays still work."},
	"connection-relay-call-denied": {kindNetwork, "The connection relay {detail} has an allow list, so the relay of your call cannot hold a place there: its key is new for each call. Guests who cannot connect to you directly need another connection relay, one without an allow list."},
	"connection-relay-wrong-key":   {kindNetwork, "The connection relay at {detail} answered with another key. Check its key in -relay, -relay-file or KOLO_MEET_RELAYS."},
	"connection-relay-bad-key":     {kindNetwork, "The key of the connection relay {detail} cannot be used. Fix it in -relay, -relay-file or KOLO_MEET_RELAYS."},
	"connection-relay-unreachable": {kindNetwork, "Could not reach the connection relay {detail}. Kolo Meet tries again."},
	"connection-relay-none-shared": {kindNetwork, "You and the host have no connection relay in common. Two people who cannot connect directly need a connection relay that both of them start Kolo Meet with (-relay), or they stay \"waiting for keys\" for each other. Ask the host for one of theirs."},
	"host-unreachable":             {kindNotFound, "Could not connect to the host's app, directly or through a connection relay. The host may have closed it or ended the call; if not, and you are on different networks, you and the host need the same connection relay (-relay)."},
	"host-unreachable-relays":      {kindNotFound, "Could not connect to the host's app, directly or through your connection relays. The host may have closed it or ended the call; if not, you and the host must start Kolo Meet with the same connection relay (-relay), with the key it prints."},
	"relay-no-common":              {kindNetwork, "Could not connect to the host's app directly, and you and the host have no connection relay in common. Start Kolo Meet again with one of the host's connection relays (-relay), then join again."},
	"internal":                     {kindInternal, "Something went wrong: {cause}."},
}

// MeetError is what the window shows. Reason is the stable ID of the sentence and Kind its group; Detail and Cause
// fill the sentence; Message is the sentence in English. Error returns Message, so a rejected binding carries it;
// Unwrap gives the cause for errors.Is.
type MeetError struct {
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
	Cause   string `json:"cause"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *MeetError) Error() string { return e.Message }
func (e *MeetError) Unwrap() error { return e.Err }

// meetErr makes the MeetError of reason. cause is the plain text of err when err is not one of the app's
// sentinels, so a sentence never shows "kolo-meet: ...".
func meetErr(reason string, err error, detail string) *MeetError {
	cause := ""
	if err != nil {
		cause = plainCause(err)
	}
	return &MeetError{Kind: reasons[reason].kind, Reason: reason, Detail: detail, Cause: cause, Message: sentence(reason, detail, cause), Err: err}
}

// sentence fills the English text of reason.
func sentence(reason, detail, cause string) string {
	return strings.NewReplacer("{detail}", detail, "{cause}", cause).Replace(reasons[reason].text)
}

// plainCause is the text of err for a sentence: the words of the OS for the common cases, without the
// "kolo-meet: " of the app's sentinels.
func plainCause(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "it does not exist"
	}
	return strings.TrimPrefix(err.Error(), "kolo-meet: ")
}

// addrInUse reports whether err is a bind on an address another socket holds. Windows reports it as
// WSAEADDRINUSE (10048), not as syscall.EADDRINUSE.
func addrInUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.EADDRINUSE || runtime.GOOS == "windows" && errno == 10048)
}

// explain turns any error of a call into a MeetError.
func explain(err error) *MeetError {
	var me *MeetError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &me):
		return me
	case errors.Is(err, context.Canceled):
		return meetErr("cancelled", err, "")
	case errors.Is(err, ErrInvalidInvite), errors.Is(err, invite.ErrInvalidCode):
		return meetErr("invalid-invite", err, "")
	case errors.Is(err, invite.ErrDenied):
		return meetErr("join-denied", err, "")
	case errors.Is(err, invite.ErrExpired):
		return meetErr("invite-expired", err, "")
	case errors.Is(err, invite.ErrUsedUp):
		return meetErr("invite-used-up", err, "")
	case errors.Is(err, invite.ErrRevoked):
		return meetErr("invite-revoked", err, "")
	case errors.Is(err, invite.ErrInvalid):
		return meetErr("invite-unknown", err, "")
	case errors.Is(err, invite.ErrRateLimited):
		return meetErr("join-rate-limited", err, "")
	case errors.Is(err, invite.ErrBusy):
		return meetErr("host-busy", err, "")
	case errors.Is(err, invite.ErrTooLarge), errors.Is(err, invite.ErrProtocol), errors.Is(err, ErrBadGrant):
		return meetErr("bad-grant", err, "")
	case errors.Is(err, invite.ErrNotAnswered):
		return meetErr("host-lost", err, "")
	case errors.Is(err, ErrNotFound), errors.Is(err, invite.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		return meetErr("host-not-found", err, "")
	case errors.Is(err, ErrNotReady):
		return meetErr("network-not-ready", err, "")
	case errors.Is(err, ErrBusy):
		return meetErr("busy-call", err, "")
	case errors.Is(err, ErrNoCall):
		return meetErr("no-call", err, "")
	case errors.Is(err, ErrClosed), errors.Is(err, koloswarm.ErrClosed), errors.Is(err, kolomoq.ErrClosed):
		return meetErr("app-closed", err, "")
	case errors.Is(err, ErrUnknownDevice):
		return meetErr("device-unknown", err, "")
	}
	return meetErr("internal", err, "")
}
