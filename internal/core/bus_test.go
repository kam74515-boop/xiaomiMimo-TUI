package core

import "testing"

func drain(ch <-chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestSubscribeReceivesAll(t *testing.T) {
	b := NewBus()
	sub := b.Subscribe(8)
	b.Publish(NewEvent(EventMessageDelta))
	b.Publish(NewEvent(EventGoalUpdate))
	if got := drain(sub); len(got) != 2 {
		t.Fatalf("unfiltered subscriber got %d events, want 2", len(got))
	}
}

func TestSubscribeFilteredDropsUnwanted(t *testing.T) {
	b := NewBus()
	control := b.SubscribeFiltered(8, EventGoalSet, EventGoalUpdate, EventMemoryWrite)

	// Flood with deltas that must NOT occupy the control buffer.
	for i := 0; i < 100; i++ {
		b.Publish(NewEvent(EventMessageDelta))
	}
	// Then publish the control events we care about.
	b.Publish(NewEvent(EventGoalSet))
	b.Publish(NewEvent(EventMemoryWrite))

	got := drain(control)
	if len(got) != 2 {
		t.Fatalf("filtered subscriber got %d events, want exactly 2 control events", len(got))
	}
	for _, e := range got {
		if e.Type != EventGoalSet && e.Type != EventMemoryWrite {
			t.Fatalf("filtered subscriber received unwanted type %s", e.Type)
		}
	}
}

func TestSubscribeFilteredNoTypesActsLikeSubscribe(t *testing.T) {
	b := NewBus()
	sub := b.SubscribeFiltered(8)
	b.Publish(NewEvent(EventMessageDelta))
	if got := drain(sub); len(got) != 1 {
		t.Fatalf("empty filter should receive all; got %d", len(got))
	}
}
