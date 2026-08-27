package version

import (
	"strings"
	"time"
)

// ChangeKind is what happened to one path between two contents of a tree.
type ChangeKind string

const (
	// Added means the path is in the second content and not the first.
	Added ChangeKind = "added"
	// Modified means the path is in both and holds different content.
	Modified ChangeKind = "modified"
	// Removed means the path is in the first content and not the second.
	Removed ChangeKind = "removed"
)

// Change is one path that differs, and how. It says nothing about the content
// on either side: a caller that wants the bytes asks for them by version, and
// most callers of a diff only ever want the paths.
type Change struct {
	Path string
	Kind ChangeKind
}

// Version is one entry of a tree's history — what it is, what it followed, and
// what was said about it.
type Version struct {
	ID      ID
	Parent  ID
	Message string
	Time    time.Time
}

// changes is what would have to happen to the content of from for it to be the
// content of to, by path. Both sides are sorted, so one walk answers it and
// neither side has to be held in a map.
func changes(from, to []entry) []Change {
	var found []Change
	i, j := 0, 0
	for i < len(from) && j < len(to) {
		switch order := strings.Compare(from[i].Path, to[j].Path); {
		case order < 0:
			found = append(found, Change{Path: from[i].Path, Kind: Removed})
			i++
		case order > 0:
			found = append(found, Change{Path: to[j].Path, Kind: Added})
			j++
		default:
			if from[i].Digest != to[j].Digest {
				found = append(found, Change{Path: to[j].Path, Kind: Modified})
			}
			i++
			j++
		}
	}
	for ; i < len(from); i++ {
		found = append(found, Change{Path: from[i].Path, Kind: Removed})
	}
	for ; j < len(to); j++ {
		found = append(found, Change{Path: to[j].Path, Kind: Added})
	}
	return found
}
