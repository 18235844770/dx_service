package netutil

import (
	"fmt"
	"net"
)

func Subnet24(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	ipv4 := parsed.To4()
	if ipv4 == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", ipv4[0], ipv4[1], ipv4[2])
}

func SameSubnet24(ip1, ip2 string) bool {
	if ip1 == "" || ip2 == "" {
		return false
	}
	return Subnet24(ip1) == Subnet24(ip2)
}
