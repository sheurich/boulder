package email

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	emailpb "github.com/letsencrypt/boulder/email/proto"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/metrics"
	"github.com/letsencrypt/boulder/test"
)

var ctx = context.Background()

// mockPardotClientImpl is a mock implementation of PardotClient.
type mockPardotClientImpl struct {
	sync.Mutex
	CreatedContacts []string
}

// newMockPardotClientImpl returns a MockPardotClientImpl, implementing the
// PardotClient interface. Both refer to the same instance, with the interface
// for mock interaction and the struct for state inspection and modification.
func newMockPardotClientImpl() (PardotClient, *mockPardotClientImpl) {
	mockImpl := &mockPardotClientImpl{
		CreatedContacts: []string{},
	}
	return mockImpl, mockImpl
}

// SendContact adds an email to CreatedContacts.
func (m *mockPardotClientImpl) SendContact(email string) error {
	m.Lock()
	defer m.Unlock()

	m.CreatedContacts = append(m.CreatedContacts, email)
	return nil
}

func (m *mockPardotClientImpl) getCreatedContacts() []string {
	m.Lock()
	defer m.Unlock()

	// Return a copy to avoid race conditions.
	return slices.Clone(m.CreatedContacts)
}

// setup creates a new ExporterImpl, a MockPardotClientImpl, and the start and
// cleanup functions for the ExporterImpl. Call start() to begin processing the
// ExporterImpl queue and cleanup() to drain and shutdown. If start() is called,
// cleanup() must be called.
func setup() (*ExporterImpl, *mockPardotClientImpl, func(), func()) {
	mockClient, clientImpl := newMockPardotClientImpl()
	exporter := NewExporterImpl(mockClient, 1000000, 5, metrics.NoopRegisterer, blog.NewMock())
	daemonCtx, cancel := context.WithCancel(context.Background())
	return exporter, clientImpl,
		func() { exporter.Start(daemonCtx) },
		func() {
			cancel()
			exporter.Drain()
		}
}

func TestSendContacts(t *testing.T) {
	t.Parallel()

	exporter, clientImpl, start, cleanup := setup()
	start()
	defer cleanup()

	wantContacts := []string{"test@example.com", "user@example.com"}
	_, err := exporter.SendContacts(ctx, &emailpb.SendContactsRequest{
		Emails: wantContacts,
	})
	test.AssertNotError(t, err, "Error creating contacts")

	var gotContacts []string
	for range 100 {
		gotContacts = clientImpl.getCreatedContacts()
		if len(gotContacts) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	test.AssertSliceContains(t, gotContacts, wantContacts[0])
	test.AssertSliceContains(t, gotContacts, wantContacts[1])
}

func TestSendContactsQueueFull(t *testing.T) {
	t.Parallel()

	exporter, _, start, cleanup := setup()
	start()
	defer cleanup()

	var err error
	for range contactsQueueCap * 2 {
		_, err = exporter.SendContacts(ctx, &emailpb.SendContactsRequest{
			Emails: []string{"test@example.com"},
		})
		if err != nil {
			break
		}
	}
	test.AssertErrorIs(t, err, ErrQueueFull)
}

func TestSendContactsQueueDrains(t *testing.T) {
	t.Parallel()

	exporter, clientImpl, start, cleanup := setup()
	start()

	var emails []string
	for i := range 100 {
		emails = append(emails, fmt.Sprintf("test@%d.example.com", i))
	}

	_, err := exporter.SendContacts(ctx, &emailpb.SendContactsRequest{
		Emails: emails,
	})
	test.AssertNotError(t, err, "Error creating contacts")

	// Drain the queue.
	cleanup()

	test.AssertEquals(t, 100, len(clientImpl.getCreatedContacts()))
}
