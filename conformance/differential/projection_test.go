package differential

import "testing"

func TestProjectDedupesAndSorts(t *testing.T) {
	got := Project(true, []string{"join", "start", "join", "fork", "start"})
	want := []string{"fork", "join", "start"}
	if !got.Completed {
		t.Error("Completed = false, want true")
	}
	if len(got.Activities) != len(want) {
		t.Fatalf("Activities = %v, want %v", got.Activities, want)
	}
	for i := range want {
		if got.Activities[i] != want[i] {
			t.Errorf("Activities = %v, want %v", got.Activities, want)
			break
		}
	}
}

func TestProjectionEqual(t *testing.T) {
	base := Project(true, []string{"a", "b"})
	if !base.Equal(Project(true, []string{"b", "a", "b"})) {
		t.Error("equal projections reported unequal (dedup/order should not matter)")
	}
	if base.Equal(Project(false, []string{"a", "b"})) {
		t.Error("differing completion status must be unequal")
	}
	if base.Equal(Project(true, []string{"a", "c"})) {
		t.Error("differing activity sets must be unequal")
	}
	if base.Equal(Project(true, []string{"a"})) {
		t.Error("differing activity counts must be unequal")
	}
}
