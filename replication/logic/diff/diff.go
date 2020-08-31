package diff

import (
	"fmt"
	"sort"
	"strings"

	. "github.com/zrepl/zrepl/replication/logic/pdu"
)

type ConflictNoCommonAncestor struct {
	SortedSenderVersions, SortedReceiverVersions []*FilesystemVersion
}

func (c *ConflictNoCommonAncestor) Error() string {
	var buf strings.Builder
	buf.WriteString("no common snapshot or suitable bookmark between sender and receiver")
	if len(c.SortedReceiverVersions) > 0 || len(c.SortedSenderVersions) > 0 {
		buf.WriteString(":\n  sorted sender versions:\n")
		for _, v := range c.SortedSenderVersions {
			fmt.Fprintf(&buf, "    %s\n", v.RelName())
		}
		buf.WriteString("  sorted receiver versions:\n")
		for _, v := range c.SortedReceiverVersions {
			fmt.Fprintf(&buf, "    %s\n", v.RelName())
		}
	}
	return buf.String()
}

type ConflictDiverged struct {
	SortedSenderVersions, SortedReceiverVersions []*FilesystemVersion
	CommonAncestor                               *FilesystemVersion
	SenderOnly, ReceiverOnly                     []*FilesystemVersion
}

func (c *ConflictDiverged) Error() string {
	var buf strings.Builder
	buf.WriteString("the receiver's latest snapshot is not present on sender:\n")
	fmt.Fprintf(&buf, "  last common: %s\n", c.CommonAncestor.RelName())
	fmt.Fprintf(&buf, "  sender-only:\n")
	for _, v := range c.SenderOnly {
		fmt.Fprintf(&buf, "    %s\n", v.RelName())
	}
	fmt.Fprintf(&buf, "  receiver-only:\n")
	for _, v := range c.ReceiverOnly {
		fmt.Fprintf(&buf, "    %s\n", v.RelName())
	}
	return buf.String()
}

func SortVersionListByCreateTXGThenBookmarkLTSnapshot(fsvslice []*FilesystemVersion) []*FilesystemVersion {
	lesser := func(s []*FilesystemVersion) func(i, j int) bool {
		return func(i, j int) bool {
			if s[i].CreateTXG < s[j].CreateTXG {
				return true
			}
			if s[i].CreateTXG == s[j].CreateTXG {
				//  Bookmark < Snapshot
				return s[i].Type == FilesystemVersion_Bookmark && s[j].Type == FilesystemVersion_Snapshot
			}
			return false
		}
	}
	if sort.SliceIsSorted(fsvslice, lesser(fsvslice)) {
		return fsvslice
	}
	sorted := make([]*FilesystemVersion, len(fsvslice))
	copy(sorted, fsvslice)
	sort.Slice(sorted, lesser(sorted))
	return sorted
}

// conflict may be a *ConflictDiverged or a *ConflictNoCommonAncestor
func IncrementalPath(receiver, sender []*FilesystemVersion) (incPath []*FilesystemVersion, conflict error) {

	receiver = SortVersionListByCreateTXGThenBookmarkLTSnapshot(receiver)
	sender = SortVersionListByCreateTXGThenBookmarkLTSnapshot(sender)

	var mrcaCandidate struct {
		found bool
		guid  uint64
		r, s  int
	}

findCandidate:
	for r := len(receiver) - 1; r >= 0; r-- {
		for s := len(sender) - 1; s >= 0; s-- {
			if sender[s].GetGuid() == receiver[r].GetGuid() {
				mrcaCandidate.guid = sender[s].GetGuid()
				mrcaCandidate.s = s
				mrcaCandidate.r = r
				mrcaCandidate.found = true
				break findCandidate
			}
		}
	}

	// handle failure cases
	if !mrcaCandidate.found {
		return nil, &ConflictNoCommonAncestor{
			SortedSenderVersions:   sender,
			SortedReceiverVersions: receiver,
		}
	} else if mrcaCandidate.r != len(receiver)-1 {
		return nil, &ConflictDiverged{
			SortedSenderVersions:   sender,
			SortedReceiverVersions: receiver,
			CommonAncestor:         sender[mrcaCandidate.s],
			SenderOnly:             sender[mrcaCandidate.s+1:],
			ReceiverOnly:           receiver[mrcaCandidate.r+1:],
		}
	}

	// incPath is possible

	// incPath must not contain bookmarks except initial one,
	incPath = make([]*FilesystemVersion, 0, len(sender))
	incPath = append(incPath, sender[mrcaCandidate.s])
	// it's ok if incPath[0] is a bookmark, but not the subsequent ones in the incPath
	for i := mrcaCandidate.s + 1; i < len(sender); i++ {
		if sender[i].Type == FilesystemVersion_Snapshot && incPath[len(incPath)-1].Guid != sender[i].Guid {
			incPath = append(incPath, sender[i])
		}
	}
	if len(incPath) == 1 {
		// nothing to do
		incPath = incPath[1:]
	}
	return incPath, nil
}
