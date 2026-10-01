package job

import (
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// SubLinkWatchJob is the link broadcast's detector (#222): it compares the
// subscription links the users were last known to have with the links now
// and, once a burst of changes has settled, asks the admins whether to send
// the new ones.
type SubLinkWatchJob struct {
	tgbotService service.Tgbot
}

// NewSubLinkWatchJob creates the link broadcast's detector job.
func NewSubLinkWatchJob() *SubLinkWatchJob {
	return new(SubLinkWatchJob)
}

// Run looks at the links once.
func (j *SubLinkWatchJob) Run() {
	j.tgbotService.CheckSubLinks()
}
