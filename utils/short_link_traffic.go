package utils

import (
	"net/netip"
	"strings"
)

// AutomatedClickUserAgentPattern is also used by the SQL reporting filter.
const AutomatedClickUserAgentPattern = `bot|crawler|spider|facebookexternalhit|facebot|twitterbot|googlemessages|telegrambot|whatsapp|preview|linkpreview|curl|wget|python-requests|okhttp|postman|v2raytun`

// IsAutomatedShortLinkRequest deliberately omits the reporting-only Linux Chrome
// heuristic and test-link flag so ordinary browser visits continue to redirect.
func IsAutomatedShortLinkRequest(userAgent, clientIP string) bool {
	ua := strings.ToLower(userAgent)
	for _, token := range strings.Split(AutomatedClickUserAgentPattern, "|") {
		if strings.Contains(ua, token) {
			return true
		}
	}
	ip, err := netip.ParseAddr(clientIP)
	if err != nil || !ip.Is4() {
		return false
	}
	octets := ip.As4()
	return (octets[0] == 66 && (octets[1] == 102 || octets[1] == 249)) ||
		(octets[0] == 74 && octets[1] == 125) ||
		(octets[0] == 192 && octets[1] == 178)
}
