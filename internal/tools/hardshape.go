package tools

import (
	"net/url"
	"strings"
)

// hardshape.go classifies the URL shapes the hard-URL ladder meets, because they
// fail in completely different ways and deserve different budgets.
//
// Measured on live runs: every LinkedIn *person* profile escalated without a
// session failed both ladder steps in turn, while posts and company pages were
// read on the first step (160, 457 and 2,919 words). A logged-out person profile
// is an authwall — time does not clear it — whereas a post or a company page is
// public and an interstitial on it does clear with an extended settle. The shape
// is therefore the cheapest available predictor of whether an escalation can pay
// off, and it costs nothing to compute.

// HardURLShape names a URL shape for escalation decisions.
type HardURLShape string

const (
	// ShapeLinkedInPerson is a person profile (/in/…): the most aggressively
	// walled shape LinkedIn serves to a logged-out browser.
	ShapeLinkedInPerson HardURLShape = "linkedin-person"
	// ShapeLinkedInDirectory is a public name directory (/pub/dir/…). It is not a
	// document about one person at all, and it is walled like a profile.
	ShapeLinkedInDirectory HardURLShape = "linkedin-directory"
	// ShapeLinkedInPost is a public post or article (/posts/, /pulse/), readable
	// without a session.
	ShapeLinkedInPost HardURLShape = "linkedin-post"
	// ShapeLinkedInCompany is a public company or school page.
	ShapeLinkedInCompany HardURLShape = "linkedin-company"
	// ShapeOther is everything else.
	ShapeOther HardURLShape = "other"
)

// HardURLShapeOf classifies a URL by path shape. Only the host is used for the
// LinkedIn test, so country subdomains (in.linkedin.com) classify the same way.
func HardURLShapeOf(targetURL string) HardURLShape {
	parsed, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return ShapeOther
	}
	if !strings.Contains(strings.ToLower(parsed.Hostname()), "linkedin.") {
		return ShapeOther
	}
	path := strings.ToLower(parsed.Path)
	switch {
	case strings.HasPrefix(path, "/pub/dir/"):
		return ShapeLinkedInDirectory
	case strings.HasPrefix(path, "/in/"):
		return ShapeLinkedInPerson
	case strings.HasPrefix(path, "/posts/"), strings.HasPrefix(path, "/pulse/"), strings.HasPrefix(path, "/feed/update/"):
		return ShapeLinkedInPost
	case strings.HasPrefix(path, "/company/"), strings.HasPrefix(path, "/school/"):
		return ShapeLinkedInCompany
	default:
		return ShapeOther
	}
}

// EscalationPriority orders candidates for the ladder's limited slots: lowest
// first. The public shapes that render in one cheap step come first, and the
// auth-walled shapes — which need a session to be readable at all — come last, so
// a run spends its two slots on pages it can actually keep.
func (shape HardURLShape) EscalationPriority() int {
	switch shape {
	case ShapeLinkedInPost:
		return 0
	case ShapeLinkedInCompany:
		return 1
	case ShapeOther:
		return 2
	case ShapeLinkedInPerson:
		return 3
	default:
		return 4
	}
}

// AuthWalled reports whether only a session (a bridged cookie) can clear this
// shape. Waiting longer cannot: the wall is an authwall, not a slow interstitial.
func (shape HardURLShape) AuthWalled() bool {
	return shape == ShapeLinkedInPerson || shape == ShapeLinkedInDirectory
}
