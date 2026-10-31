package deal

import "regexp"

// pageBundleElement is the JSON data element a page built by capsule-viewer
// 0.4.0 or later carries its bundle in (capsule-viewer#109).
var pageBundleElement = regexp.MustCompile(`(?s)<script type="application/json">(.*?)</script>`)

// pageBundleText is the bundle's JSON as the page's data element holds it,
// and whether the page has one.
func pageBundleText(page string) (string, bool) {
	m := pageBundleElement.FindStringSubmatch(page)
	if m == nil {
		return "", false
	}
	return m[1], true
}
