package main

import "testing"

func TestFinishChoicesWaitForParentAndStayIndependent(t *testing.T) {
	for _, d := range []bool{false, true} {
		for _, c := range []bool{false, true} {
			for _, exited := range []bool{false, true} {
				waited, ds, cs := false, 0, 0
				h := finishHooks{WaitParent: func() bool { waited = true; return exited }, DiscordRunning: func() bool { return false }, CompanionRunning: func() bool { return false }, StartDiscord: func() error {
					if !waited || !exited {
						t.Fatal("early Discord")
					}
					ds++
					return nil
				}, StartCompanion: func() error {
					if !waited || !exited {
						t.Fatal("early Companion")
					}
					cs++
					return nil
				}}
				finishHandoff(d, c, h)
				if (ds == 1) != (d && exited) || (cs == 1) != (c && exited) {
					t.Fatalf("choices %v/%v, parent %v: %d/%d", d, c, exited, ds, cs)
				}
			}
		}
	}
}
