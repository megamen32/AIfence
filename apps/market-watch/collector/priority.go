package catalog

import (
	"net/url"
	"sort"
	"strings"
)

// Explicit demo scope is a scheduling preference, not product identification.
// No SKU, category, price or completeness inference follows from a URL match.
func DemoPriority(raw string) int {
	u, e := url.Parse(raw)
	if e != nil {
		return 0
	}
	path := strings.ToLower(u.Path)
	for _, v := range []string{"kaspersky", "drweb", "dr-web", "doctor-web", "astralinux", "astra-linux", "myoffice", "my-office", "moy-ofis", "moyofis", "novyy-disk", "moj-ofis"} {
		if strings.Contains(path, v) {
			return 100
		}
	}
	return 0
}
func PrioritizeURLs(urls []string) {
	sort.SliceStable(urls, func(i, j int) bool { return DemoPriority(urls[i]) > DemoPriority(urls[j]) })
}
