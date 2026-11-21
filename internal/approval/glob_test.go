package approval

import (
	"encoding/json"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGlobMatches(t *testing.T) {
	assert.True(t, MustGlob("/usr/bin/*").Matches("/usr/bin/git"))
	assert.False(t, MustGlob("/usr/bin/*").Matches("/usr/bin/sub/git"))
	assert.True(t, Glob{}.Matches(""))
	assert.False(t, Glob{}.Matches("x"))
}

func TestParseGlob(t *testing.T) {
	_, err := ParseGlob("")
	assert.ErrorIs(t, err, errEmptyGlob)
	_, err = ParseGlob("[")
	assert.ErrorIs(t, err, path.ErrBadPattern)
	assert.Panics(t, func() { MustGlob("[") })
}

func TestGlobUnmarshalRejectsMalformed(t *testing.T) {
	var m ProcessMatcher
	require.NoError(t, json.Unmarshal([]byte(`{"exe": "/usr/bin/[gh]it"}`), &m))
	assert.Equal(t, MustGlob("/usr/bin/[gh]it"), m.Exe)

	assert.ErrorIs(t, json.Unmarshal([]byte(`{"exe": "["}`), &m), path.ErrBadPattern)
}

func TestGlobUnmarshalRejectsEmpty(t *testing.T) {
	var m ProcessMatcher
	assert.ErrorIs(t, json.Unmarshal([]byte(`{"exe": ""}`), &m), errEmptyGlob)
}

// Pins the config and websocket shapes of a trust rule: every glob is a string,
// and an unset glob is absent.
func TestTrustRuleRoundTrip(t *testing.T) {
	const yamlText = `name: r
action: deny
client: socket:*
process:
    exe: /usr/bin/*
    unit: app.service
secret:
    label: x*
    attributes:
        service: gh*
search_attributes:
    user: me
`
	const jsonText = `{"name":"r","action":"deny","client":"socket:*","process":{"exe":"/usr/bin/*","unit":"app.service"},"secret":{"label":"x*","attributes":{"service":"gh*"}},"search_attributes":{"user":"me"}}`

	var fromYAML TrustRule
	require.NoError(t, yaml.Unmarshal([]byte(yamlText), &fromYAML))
	out, err := yaml.Marshal(fromYAML)
	require.NoError(t, err)
	assert.Equal(t, yamlText, string(out))

	var fromJSON TrustRule
	require.NoError(t, json.Unmarshal([]byte(jsonText), &fromJSON))
	assert.Equal(t, fromYAML, fromJSON)
	out, err = json.Marshal(fromJSON)
	require.NoError(t, err)
	assert.JSONEq(t, jsonText, string(out))
	assert.Equal(t, jsonText, string(out))

	assert.True(t, fromJSON.Process.Exe.Matches("/usr/bin/git"))
	assert.False(t, fromJSON.Process.Name.Matches("git"))
}
