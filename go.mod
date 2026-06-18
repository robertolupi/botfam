module github.com/robertolupi/botfam

go 1.26.0

require (
	gitea.dev/sdk v1.1.0
	github.com/mark3labs/mcp-go v0.54.1
	github.com/openai/openai-go/v2 v2.7.1
	github.com/pelletier/go-toml/v2 v2.3.1
	github.com/spf13/cobra v1.9.1
	golang.org/x/term v0.44.0
)

require (
	bitbucket.org/creachadair/stringset v0.0.11 // indirect
	github.com/42wim/httpsig v1.2.4 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/chzyer/readline v1.5.1 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/exp v0.0.0-20240707233637-46b078467d37 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

require (
	codeberg.org/TauCeti/mangle-go v0.0.0-00010101000000-000000000000
	gitea.com/gitea/gitea-mcp v0.0.0
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace codeberg.org/TauCeti/mangle-go => ./third_party/mangle-go

replace gitea.com/gitea/gitea-mcp => ./third_party/gitea-mcp
