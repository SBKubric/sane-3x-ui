package controller

import "strings"

// oneLine keeps what a client typed on the one log line it is written to.
//
// The panel logs a failed login with the username and password as typed, and
// fail2ban's login jail (#141) bans the address at the end of that line. A
// line break in the username used to start a new line of the attacker's own
// making; the quotes are escaped, but a log that anyone can add lines to is
// not one to ban from.
func oneLine(s string) string {
	return strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(s)
}
