// Copyright (c) Liam Stanley <me@liamstanley.io>. All rights reserved. Use
// of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package girc

import "strings"

// CMode represents a single step of a given mode change.
type CMode struct {
	add     bool   // if it's a +, or -.
	name    byte   // character representation of the given mode.
	setting bool   // if it's a setting (should be stored) or temporary (op/voice/etc).
	args    string // arguments to the mode, if arguments are supported.
}

// Short returns a short representation of a mode without arguments. E.g. "+a",
// or "-b".
func (c *CMode) Short() string {
	var status string
	if c.add {
		status = "+"
	} else {
		status = "-"
	}

	return status + string(c.name)
}

// String returns a string representation of a mode, including optional
// arguments. E.g. "+b user*!ident@host.*.com"
func (c *CMode) String() string {
	if len(c.args) == 0 {
		return c.Short()
	}

	return c.Short() + " " + c.args
}

// CModes is a representation of a set of modes. This may be the given state
// of a channel/user, or the given state changes to a given channel/user.
type CModes struct {
	raw           string // raw supported modes.
	modesListArgs string // modes that add/remove users from lists and support args.
	modesArgs     string // modes that support args.
	modesSetArgs  string // modes that support args ONLY when set.
	modesNoArgs   string // modes that do not support args.

	prefixes string  // user permission prefixes. these aren't a CMode.setting.
	modes    []CMode // the list of modes for this given state.
}

// String returns a complete set of modes for this given state (change?). For
// example, "+a-b+cde some-arg".
func (c *CModes) String() string {
	var out string
	var args string

	if len(c.modes) > 0 {
		out += "+"
	}

	for i := 0; i < len(c.modes); i++ {
		out += string(c.modes[i].name)

		if len(c.modes[i].args) > 0 {
			args += " " + c.modes[i].args
		}
	}

	return out + args
}

// HasMode checks if the CModes state has a given mode. E.g. "m", or "I".
func (c *CModes) HasMode(mode string) bool {
	for i := 0; i < len(c.modes); i++ {
		if string(c.modes[i].name) == mode {
			return true
		}
	}

	return false
}

// Get returns the arguments for a given mode within this session, if it
// supports args.
func (c *CModes) Get(mode string) (args string, ok bool) {
	for i := 0; i < len(c.modes); i++ {
		if string(c.modes[i].name) == mode {
			if len(c.modes[i].args) == 0 {
				return "", false
			}

			return c.modes[i].args, true
		}
	}

	return "", false
}

// hasArg checks to see if the mode supports arguments. What ones support this?:
//   A = Mode that adds or removes a nick or address to a list. Always has a parameter.
//   B = Mode that changes a setting and always has a parameter.
//   C = Mode that changes a setting and only has a parameter when set.
//   D = Mode that changes a setting and never has a parameter.
//   Note: Modes of type A return the list when there is no parameter present.
//   Note: Some clients assumes that any mode not listed is of type D.
//   Note: Modes in PREFIX are not listed but could be considered type B.
func (c *CModes) hasArg(set bool, mode byte) (hasArgs, isSetting bool) {
	if len(c.raw) < 1 {
		return false, true
	}

	if strings.IndexByte(c.modesListArgs, mode) > -1 {
		return true, false
	}

	if strings.IndexByte(c.modesArgs, mode) > -1 {
		return true, true
	}

	if strings.IndexByte(c.modesSetArgs, mode) > -1 {
		if set {
			return true, true
		}

		return false, true
	}

	if strings.IndexByte(c.prefixes, mode) > -1 {
		return true, false
	}

	return false, true
}

// apply merges two state changes, or one state change into a state of modes.
// For example, the latter would mean applying an incoming MODE with the modes
// stored for a channel.
func (c *CModes) apply(modes []CMode) {
	var new []CMode

	for j := 0; j < len(c.modes); j++ {
		isin := false
		for i := 0; i < len(modes); i++ {
			if !modes[i].setting {
				continue
			}
			if c.modes[j].name == modes[i].name && modes[i].add {
				new = append(new, modes[i])
				isin = true
				break
			}
		}

		if !isin {
			new = append(new, c.modes[j])
		}
	}

	for i := 0; i < len(modes); i++ {
		if !modes[i].setting || !modes[i].add {
			continue
		}

		isin := false
		for j := 0; j < len(new); j++ {
			if modes[i].name == new[j].name {
				isin = true
				break
			}
		}

		if !isin {
			new = append(new, modes[i])
		}
	}

	c.modes = new
}

// parse parses a set of flags and args, returning the necessary list of
// mappings for the mode flags.
func (c *CModes) parse(flags string, args []string) (out []CMode) {
	// add is the mode state we're currently in. Adding, or removing modes.
	add := true
	var argCount int

	for i := 0; i < len(flags); i++ {
		if flags[i] == 0x2B {
			add = true
			continue
		}
		if flags[i] == 0x2D {
			add = false
			continue
		}

		mode := CMode{
			name: flags[i],
			add:  add,
		}

		hasArgs, isSetting := c.hasArg(add, flags[i])
		if hasArgs && len(args) >= argCount+1 {
			mode.args = args[argCount]
			argCount++
		}
		mode.setting = isSetting

		out = append(out, mode)
	}

	return out
}

// newCModes returns a new CModes reference. channelModes and userPrefixes
// would be something you see from the server's "CHANMODES" and "PREFIX"
// ISUPPORT capability messages (alternatively, fall back to the standard)
// DefaultPrefixes and ModeDefaults.
func newCModes(channelModes, userPrefixes string) CModes {
	split := strings.SplitN(channelModes, ",", 4)
	if len(split) != 4 {
		for i := len(split); i < 4; i++ {
			split = append(split, "")
		}
	}

	return CModes{
		raw:           channelModes,
		modesListArgs: split[0],
		modesArgs:     split[1],
		modesSetArgs:  split[2],
		modesNoArgs:   split[3],

		prefixes: userPrefixes,
		modes:    []CMode{},
	}
}

// isValidChannelMode validates a channel mode (CHANMODES).
func isValidChannelMode(raw string) bool {
	if len(raw) < 1 {
		return false
	}

	for i := 0; i < len(raw); i++ {
		// Allowed are: ",", A-Z and a-z.
		if raw[i] != 0x2C && (raw[i] < 0x41 || raw[i] > 0x5A) && (raw[i] < 0x61 || raw[i] > 0x7A) {
			return false
		}
	}

	return true
}

// isValidUserPrefix validates a list of ISUPPORT-style user prefixes (PREFIX).
func isValidUserPrefix(raw string) bool {
	if len(raw) < 1 {
		return false
	}

	if raw[0] != 0x28 { // (.
		return false
	}

	var keys, rep int
	var passedKeys bool

	// Skip the first one as we know it's (.
	for i := 1; i < len(raw); i++ {
		if raw[i] == 0x29 { // ).
			passedKeys = true
			continue
		}

		if passedKeys {
			rep++
		} else {
			keys++
		}
	}

	return keys == rep
}

// parsePrefixes parses the mode character mappings from the symbols of a
// ISUPPORT-style user prefixes list (PREFIX).
func parsePrefixes(raw string) (modes, prefixes string) {
	if !isValidUserPrefix(raw) {
		return modes, prefixes
	}

	i := strings.Index(raw, ")")
	if i < 1 {
		return modes, prefixes
	}

	return raw[1:i], raw[i+1:]
}

// handleMODE handles incoming MODE messages, and updates the tracking
// information for each channel, as well as if any of the modes affect user
// permissions.
func handleMODE(c *Client, e Event) {
	// Check if it's a RPL_CHANNELMODEIS.
	if e.Command == RPL_CHANNELMODEIS && len(e.Params) > 2 {
		// RPL_CHANNELMODEIS sends the user as the first param, skip it.
		e.Params = e.Params[1:]
	}
	// Should be at least MODE <target> <flags>, to be useful. As well, only
	// tracking channel modes at the moment.
	if len(e.Params) < 2 || !IsValidChannel(e.Params[0]) {
		return
	}

	c.state.mu.Lock()
	channel := c.state.lookupChannel(e.Params[0])
	if channel == nil {
		c.state.mu.Unlock()
		return
	}

	flags := e.Params[1]
	var args []string
	if len(e.Params) > 2 {
		args = append(args, e.Params[2:]...)
	}

	modes := channel.Modes.parse(flags, args)
	channel.Modes.apply(modes)

	// Loop through and update users modes as necessary.
	for i := 0; i < len(modes); i++ {
		if modes[i].setting || len(modes[i].args) == 0 {
			continue
		}

		users := c.state.lookupUsers("nick", modes[i].args)
		for j := 0; j < len(users); j++ {
			users[j].Perms.setFromMode(modes[i])
		}
	}

	c.state.mu.Unlock()
}

// chanModes returns the ISUPPORT list of server-supported channel modes,
// alternatively falling back to ModeDefaults.
func (s *state) chanModes() string {
	if modes, ok := s.serverOptions["CHANMODES"]; ok && isValidChannelMode(modes) {
		return modes
	}

	return ModeDefaults
}

// userPrefixes returns the ISUPPORT list of server-supported user prefixes.
// This includes mode characters, as well as user prefix symbols. Falls back
// to DefaultPrefixes if not server-supported.
func (s *state) userPrefixes() string {
	if prefix, ok := s.serverOptions["PREFIX"]; ok && isValidUserPrefix(prefix) {
		return prefix
	}

	return DefaultPrefixes
}

// UserPerms contains all channel-based user permissions. The minimum op, and
// voice should be supported on all networks. This also supports non-rfc
// Owner, Admin, and HalfOp, if the network has support for it.
type UserPerms struct {
	// Owner (non-rfc) indicates that the user has full permissions to the
	// channel. More than one user can have owner permission.
	Owner bool
	// Admin (non-rfc) is commonly given to users that are trusted enough
	// to manage channel permissions, as well as higher level service settings.
	Admin bool
	// Op is commonly given to trusted users who can manage a given channel
	// by kicking, and banning users.
	Op bool
	// HalfOp (non-rfc) is commonly used to give users permissions like the
	// ability to kick, without giving them greater abilities to ban all users.
	HalfOp bool
	// Voice indicates the user has voice permissions, commonly given to known
	// users, with very light trust, or to indicate a user is active.
	Voice bool
}

// IsAdmin indicates that the user has banning abilities, and are likely a
// very trustable user (e.g. op+).
func (m UserPerms) IsAdmin() bool {
	if m.Owner || m.Admin || m.Op {
		return true
	}

	return false
}

// IsTrusted indicates that the user at least has modes set upon them, higher
// than a regular joining user.
func (m UserPerms) IsTrusted() bool {
	if m.IsAdmin() || m.HalfOp || m.Voice {
		return true
	}

	return false
}

// reset resets the modes of a user.
func (m *UserPerms) reset() {
	m.Owner = false
	m.Admin = false
	m.Op = false
	m.HalfOp = false
	m.Voice = false
}

// set translates raw prefix characters into proper permissions. Only
// use this function when you have a session lock.
func (m *UserPerms) set(prefix string, append bool) {
	if !append {
		m.reset()
	}

	for i := 0; i < len(prefix); i++ {
		switch string(prefix[i]) {
		case OwnerPrefix:
			m.Owner = true
		case AdminPrefix:
			m.Admin = true
		case OperatorPrefix:
			m.Op = true
		case HalfOperatorPrefix:
			m.HalfOp = true
		case VoicePrefix:
			m.Voice = true
		}
	}
}

// setFromMode sets user-permissions based on channel user mode chars. E.g.
// "o" being oper, "v" being voice, etc.
func (m *UserPerms) setFromMode(mode CMode) {
	switch string(mode.name) {
	case ModeOwner:
		m.Owner = mode.add
	case ModeAdmin:
		m.Admin = mode.add
	case ModeOperator:
		m.Op = mode.add
	case ModeHalfOperator:
		m.HalfOp = mode.add
	case ModeVoice:
		m.Voice = mode.add
	}
}

// parseUserPrefix parses a raw mode line, like "@user" or "@+user".
func parseUserPrefix(raw string) (modes, nick string, success bool) {
	for i := 0; i < len(raw); i++ {
		char := string(raw[i])

		if char == OwnerPrefix || char == AdminPrefix || char == HalfOperatorPrefix ||
			char == OperatorPrefix || char == VoicePrefix {
			modes += char
			continue
		}

		// Assume we've gotten to the nickname part.
		return modes, raw[i:], true
	}

	return
}
