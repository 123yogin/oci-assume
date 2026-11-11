// Package regions lists OCI region identifiers for the picker and completion.
package regions

// Known is a static, sorted list of OCI public commercial (OC1) regions.
// It can go stale; region names are validated by regex anyway, so any region still works.
var Known = []string{
	"af-casablanca-1", "af-johannesburg-1",
	"ap-batam-1", "ap-chuncheon-1", "ap-hyderabad-1", "ap-kulai-2", "ap-melbourne-1", "ap-mumbai-1",
	"ap-osaka-1", "ap-seoul-1", "ap-singapore-1", "ap-singapore-2", "ap-sydney-1", "ap-tokyo-1",
	"ca-montreal-1", "ca-toronto-1",
	"eu-amsterdam-1", "eu-frankfurt-1", "eu-madrid-1", "eu-madrid-3", "eu-marseille-1", "eu-milan-1",
	"eu-paris-1", "eu-stockholm-1", "eu-turin-1", "eu-zurich-1",
	"il-jerusalem-1",
	"me-abudhabi-1", "me-dubai-1", "me-jeddah-1", "me-riyadh-1",
	"mx-monterrey-1", "mx-queretaro-1",
	"sa-bogota-1", "sa-santiago-1", "sa-saopaulo-1", "sa-valparaiso-1", "sa-vinhedo-1",
	"uk-cardiff-1", "uk-london-1",
	"us-ashburn-1", "us-chicago-1", "us-phoenix-1", "us-sanjose-1",
}

// Merge concatenates groups, dropping empty names and later duplicates.
func Merge(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		for _, r := range g {
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}
