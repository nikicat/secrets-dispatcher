module github.com/nikicat/secrets-dispatcher

go 1.26

require (
	github.com/coder/websocket v1.8.14
	github.com/fsnotify/fsnotify v1.9.0
	github.com/godbus/dbus/v5 v5.2.2
	github.com/google/uuid v1.6.0
	github.com/lmittmann/tint v1.1.3
	golang.org/x/sys v0.41.0
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/crypto v0.48.0

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.31.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/tools v0.40.1-0.20260108161641-ca281cf95054 // indirect
	honnef.co/go/tools v0.7.0 // indirect
)

tool honnef.co/go/tools/cmd/staticcheck
