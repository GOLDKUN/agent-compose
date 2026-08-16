package assets

import "embed"

//go:embed .codex .claude .claude.json .gitconfig .dsh
var DefaultHomeFS embed.FS
