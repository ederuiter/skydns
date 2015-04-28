// Copyright (c) 2014 The SkyDNS Authors. All rights reserved.
// Use of this source code is governed by The MIT License (MIT) that can be
// found in the LICENSE file.

package msg

import (
	"net"
	"path"
	"strings"

	"github.com/miekg/dns"
)

// This *is* the rdata from a SRV record, but with a twist.
// Host (Target in SRV) must be a domain name, but if it looks like an IP
// address (4/6), we will treat it like an IP address.
type Service struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	Text     string `json:"text,omitempty"`
	Ttl      uint32 `json:"ttl,omitempty"`

	// When a SRV record with a "Host: IP-address" is added, we synthesize
	// a srv.Target domain name.  Normally we convert the full Key where
	// the record lives to a DNS name and use this as the srv.Target.  When
	// TargetStrip > 0 we strip the left most TargetStrip labels from this
	// synthesized DNS name.
	TargetStrip int `json:"targetstrip",omitempty"`

	// Group is used to group (or *not* to group) different services
	// together. Services with an identical Group are returned in the same
	// answer.
	Group string `json:"group,omitempty"`

	// Etcd key where we found this service and ignored from json un-/marshalling
	Key string `json:"-"`
}

// NewSRV returns a new SRV record based on the Service.
func (s *Service) NewSRV(name string, weight uint16) *dns.SRV {
	host := dns.Fqdn(s.Host)

	offset, end := 0, false
	for i := 0; i < s.TargetStrip; i++ {
		offset, end = dns.NextLabel(host, offset)
	}
	if end {
		// We overshot the name, use the orignal one.
		offset = 0
	}
	host = host[offset:]

	return &dns.SRV{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: s.Ttl},
		Priority: uint16(s.Priority), Weight: weight, Port: uint16(s.Port), Target: host}
}

// NewA returns a new A record based on the Service.
func (s *Service) NewA(name string, ip net.IP) *dns.A {
	return &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: s.Ttl}, A: ip}
}

// NewAAAA returns a new AAAA record based on the Service.
func (s *Service) NewAAAA(name string, ip net.IP) *dns.AAAA {
	return &dns.AAAA{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: s.Ttl}, AAAA: ip}
}

// NewCNAME returns a new CNAME record based on the Service.
func (s *Service) NewCNAME(name string, target string) *dns.CNAME {
	return &dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: s.Ttl}, Target: target}
}

// NewNS returns a new NS record based on the Service.
func (s *Service) NewNS(name string, target string) *dns.NS {
	return &dns.NS{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: s.Ttl}, Ns: target}
}

// NewTXT returns a new TXT record based on the Service.
func (s *Service) NewTXT(name string) *dns.TXT {
	return &dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: s.Ttl}, Txt: split255(s.Text)}
}

// NewPTR returns a new PTR record based on the Service.
func (s *Service) NewPTR(name string, ttl uint32) *dns.PTR {
	return &dns.PTR{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: ttl}, Ptr: dns.Fqdn(s.Host)}
}

// As Path, but if a name contains wildcards (*), the name will be chopped of
// before the (first) wildcard, and we do a highler evel search and later find
// the matching names.  So service.*.skydns.local, will look for all services
// under skydns.local and will later check for names that match
// service.*.skydns.local.  If a wildcard is found the returned bool is true.
func PathWithWildcard(s string) (string, bool) {
	l := dns.SplitDomainName(s)
	for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
	for i, k := range l {
		if k == "*" {
			return path.Join(append([]string{"/skydns/"}, l[:i]...)...), true
		}
	}
	return path.Join(append([]string{"/skydns/"}, l...)...), false
}

// Path converts a domainname to an etcd path. If s looks like service.staging.skydns.local.,
// the resulting key will be /skydns/local/skydns/staging/service .
func Path(s string) string {
	l := dns.SplitDomainName(s)
	for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
	return path.Join(append([]string{"/skydns/"}, l...)...)
}

// Domain is the opposite of Path.
func Domain(s string) string {
	l := strings.Split(s, "/")
	// start with 1, to strip /skydns
	for i, j := 1, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
	return dns.Fqdn(strings.Join(l[1:len(l)-1], "."))
}

// Group checks the services in sx, looks for a Group attribute in the first
// ones (i.e. all services with same path length) and (if found) selects only
// those services with the same group.
func Group(sx []Service) []Service {
	if len(sx) == 0 {
		return sx
	}

	slashes := strings.Count(sx[0].Key, "/")
	group := ""
	count := true

	ret := []Service{} // with slice-tricks in sx we can save this allocation (TODO)

	// Now now want to look at the first N services of length slashes
	// that either share Group or are empty
	for i, s := range sx {
		if count && strings.Count(s.Key, "/") == slashes {
			if s.Group == "" {
				continue
			}
			if group == "" {
				group = s.Group
				continue
			}

			if s.Group != "" && s.Group != group {
				// one of the groups on the same level does not
				// agree with 'group'. The Group thing does not
				// apply to these set of services.
				return sx
			}
		}
		// Now we are decending a level. If group is still empty, we
		// are not doing groups.
		if group == "" {
			return sx
		}

		// We've established a group, this means all services up to i
		// should be included, but only if count=true (i.e. the first time)
		if count {
			ret = sx[:i]
			count = false
		}

		if s.Group == "" || s.Group == group {
			ret = append(ret, s)
		}
	}

	return ret
}

// Split255 splits a string into 255 byte chunks.
func split255(s string) []string {
	if len(s) < 255 {
		return []string{s}
	}
	sx := []string{}
	p, i := 0, 255
	for {
		if i <= len(s) {
			sx = append(sx, s[p:i])
		} else {
			sx = append(sx, s[p:])
			break

		}
		p, i = p+255, i+255
	}

	return sx
}
