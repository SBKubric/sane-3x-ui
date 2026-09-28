package main

import (
	"fmt"
	"os"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// proxyDecoy is the page a box's front serves when proxy.json names none of
// its own: the welcome page this box's nginx package installed (#160), the
// embedded copy of it otherwise.
//
// front.stub still wins (#153 Q3), but a front.stub that is one of the pages
// shipped with the panel, unchanged, is the same bytes on every box running
// it — warning says so, for the log.
func proxyDecoy(frontStub string) (html, warning string) {
	stubs := &service.StubService{}
	if frontStub != "" {
		if body, err := os.ReadFile(frontStub); err == nil {
			if tpl, ok := stubs.UpstreamTemplate(string(body)); ok {
				warning = fmt.Sprintf("front.stub %s is the built-in page «%s», unchanged — every box running this panel can serve the same bytes; drop front.stub for nginx's welcome page or point it at a page of your own", frontStub, tpl.Name)
			}
		}
	}
	if tpl, ok := stubs.DefaultTemplate(); ok {
		html = tpl.Html
	}
	return html, warning
}
