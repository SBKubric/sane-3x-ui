package job

import (
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// SubRequestExpireJob expires the requests for a subscription nobody decided
// on in 14 days and tells each person (#188 point 2, #220).
type SubRequestExpireJob struct {
	tgbotService service.Tgbot
}

// NewSubRequestExpireJob creates the requests' expiry job.
func NewSubRequestExpireJob() *SubRequestExpireJob {
	return new(SubRequestExpireJob)
}

// Run expires the due requests.
func (j *SubRequestExpireJob) Run() {
	j.tgbotService.ExpireSubRequests()
}
