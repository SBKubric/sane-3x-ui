package job

import (
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// VPNNameJob keeps the VPN name's A record on the active edge through the
// DNSExit API (#225): every look compares the record with the active edge
// and calls DNSExit when they differ, at most once every four minutes.
type VPNNameJob struct {
	vpnNameService service.VPNNameService
}

// NewVPNNameJob creates the VPN name's job.
func NewVPNNameJob() *VPNNameJob {
	return new(VPNNameJob)
}

// Run looks once.
func (j *VPNNameJob) Run() {
	j.vpnNameService.Tick()
}

// DomainExpiryJob posts the domain renewal reminder to the notification
// channel 30 and 7 days before the registration expiry date and on the day
// (#225).
type DomainExpiryJob struct {
	tgbotService service.Tgbot
}

// NewDomainExpiryJob creates the renewal reminder's job.
func NewDomainExpiryJob() *DomainExpiryJob {
	return new(DomainExpiryJob)
}

// Run posts the reminder that is due, if any.
func (j *DomainExpiryJob) Run() {
	j.tgbotService.CheckDomainExpiry()
}
