package voip

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type cleanupFake struct {
	steps    []string
	fail     string
	contacts []Contact
}

func (f *cleanupFake) step(ctx context.Context, name string) error {
	if _, ok := ctx.Deadline(); !ok {
		panic("cleanup must be bounded")
	}
	f.steps = append(f.steps, name)
	if name == f.fail {
		return errors.New("injected failure")
	}
	return nil
}
func (f *cleanupFake) Contacts(ctx context.Context, _ string) ([]Contact, error) {
	return f.contacts, f.step(ctx, "read")
}
func (f *cleanupFake) DeleteAuthorizations(ctx context.Context, _ string) error {
	if err := f.step(ctx, "delete"); err != nil {
		return err
	}
	f.contacts = nil
	return nil
}
func (f *cleanupFake) ReleaseDevice(ctx context.Context, _ string, contacts []Contact) error {
	if !reflect.DeepEqual(contacts, f.contacts) {
		panic("lost contact keys")
	}
	return f.step(ctx, "release")
}
func (f *cleanupFake) AuthChanged(ctx context.Context, _ string) error { return f.step(ctx, "notify") }

func TestCleanupFailureStopsAndCanRetry(t *testing.T) {
	for index, stage := range []string{"read", "release", "delete", "notify"} {
		t.Run(stage, func(t *testing.T) {
			f := &cleanupFake{fail: stage, contacts: []Contact{{AppID: "app", OpenID: "contact"}}}
			s := NewCleanup(f, f, f)
			if err := s.Unbind(context.Background(), "device"); err == nil {
				t.Fatal("failure swallowed")
			}
			want := []string{"read", "release", "delete", "notify"}[:index+1]
			if !reflect.DeepEqual(f.steps, want) {
				t.Fatalf("steps = %v, want %v", f.steps, want)
			}
			if index < 2 && len(f.contacts) != 1 {
				t.Fatal("contacts lost before guard release")
			}
			f.fail = ""
			for attempt := 0; attempt < 2; attempt++ {
				if err := s.Unbind(context.Background(), "device"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
