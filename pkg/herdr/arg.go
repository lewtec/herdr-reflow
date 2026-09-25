package herdr

import (
	"fmt"
	"strings"
	"time"

	"github.com/lewtec/lewkit/x/cmd"
)

// RepoBranch is a REPO:BRANCH argument. The branch may contain slashes.
// Each %d in the branch is the local date as yyyymmdd. The repo side is literal.
type RepoBranch struct {
	Repo   string
	Branch string
}

// Parse splits one REPO:BRANCH token. The first colon separates the repo.
func (r *RepoBranch) Parse(arg string) error {
	repo, branch, ok := strings.Cut(arg, ":")
	if !ok || repo == "" || branch == "" {
		return fmt.Errorf("%w: expected REPO:BRANCH, got %q", cmd.ErrInvalidArgument, arg)
	}
	r.Repo = repo
	r.Branch = strings.ReplaceAll(branch, "%d", time.Now().Format("20060102"))
	return nil
}

var _ cmd.Parser = (*RepoBranch)(nil)
