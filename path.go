package tdms

import "strings"

const (
	kindFile = iota
	kindGroup
	kindChannel
)

// quoteName quotes a group or channel name the way TDMS paths do: the name is
// wrapped in single quotes, and each single quote inside the name is doubled.
func quoteName(name string) string {
	return "'" + strings.ReplaceAll(name, "'", "''") + "'"
}

func groupPath(name string) string { return "/" + quoteName(name) }

func channelPath(group, name string) string {
	return "/" + quoteName(group) + "/" + quoteName(name)
}

// parseObjectPath splits a TDMS object path.
//
//	"/"                         file
//	"/'group'"                  group
//	"/'group'/'channel'"        channel
//
// A single quote inside a name is stored as two quotes (/'Dr. T”s Events').
func parseObjectPath(p string) (kind int, group, channel string, err error) {
	if p == "/" {
		return kindFile, "", "", nil
	}
	if len(p) == 0 || p[0] != '/' {
		return 0, "", "", ErrCorrupt
	}
	name, rest, err := parseQuoted(p[1:])
	if err != nil {
		return 0, "", "", err
	}
	if rest == "" {
		return kindGroup, name, "", nil
	}
	if rest[0] != '/' {
		return 0, "", "", ErrCorrupt
	}
	channel, rest, err = parseQuoted(rest[1:])
	if err != nil {
		return 0, "", "", err
	}
	if rest != "" {
		return 0, "", "", ErrCorrupt
	}
	return kindChannel, name, channel, nil
}

func parseQuoted(s string) (name, rest string, err error) {
	if len(s) == 0 || s[0] != '\'' {
		return "", "", ErrCorrupt
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		return b.String(), s[i+1:], nil
	}
	return "", "", ErrCorrupt
}
