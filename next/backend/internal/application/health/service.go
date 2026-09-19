package health

import domainhealth "github.com/shentschel/teddycloud/next/backend/internal/domain/health"

// Service composes health information without depending on a transport.
type Service struct {
	version string
}

func New(version string) Service {
	return Service{version: version}
}

func (s Service) Current() domainhealth.Status {
	return domainhealth.Status{State: "ok", Version: s.version}
}
