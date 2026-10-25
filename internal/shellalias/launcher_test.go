package shellalias

import "testing"

func TestEmitAlias_UsesLauncher(t *testing.T) {
	Launcher = "ccm2"
	t.Cleanup(func() { Launcher = "ccm" })

	cases := []struct {
		sh   Shell
		want string
	}{
		{newBash(), `cld() { ccm2 launch '--via' 't' -- "$@"; }`},
		{newFish(), `function cld; ccm2 launch '--via' 't' -- $argv; end`},
		{newPwsh(), `function cld { ccm2 launch '--via' 't' -- @args }`},
	}
	for _, tc := range cases {
		if got := tc.sh.EmitAlias("cld", []string{"--via", "t"}); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.sh.Name(), got, tc.want)
		}
	}
}
