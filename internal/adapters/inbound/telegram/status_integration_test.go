package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arhor/case-watch/internal/adapters/outbound/clients"
	"github.com/arhor/case-watch/internal/adapters/outbound/database"
	"github.com/arhor/case-watch/internal/adapters/outbound/database/repositories"
	"github.com/arhor/case-watch/internal/domain"
	"github.com/arhor/case-watch/internal/services"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestBotStatusIntegration(test *testing.T) {
	// Given
	environment := setup(test)
	ctx := context.Background()
	owner := int64(101)
	checkedAt := environment.clock.Now().Add(-2 * time.Minute)
	err := environment.store.TrackedCases().Create(ctx, owner, "00112233445566778899aabbccddeeff", "EXAMPLE-0001", "synthetic-password", domain.PortalSnapshot{
		Stage: "Previous stage", CheckedAt: checkedAt,
	})
	assert.NoError(test, err)

	var portalRequests []string
	var rpcReplies []string
	assert.NoError(test, json.Unmarshal(readStatusFixture(test, "../../outbound/clients/testdata/vaadin/rpc-authentication.json"), &rpcReplies))
	bootstrap, err := os.ReadFile("../../outbound/clients/testdata/vaadin/bootstrap-login.html")
	assert.NoError(test, err)
	portalClient := clients.NewGdanskPortalClient(clients.GdanskPortalClientOptions{
		BaseURL: "https://portal.offline.invalid",
		Clock:   environment.clock,
		Wait: func(ctx context.Context, delay time.Duration) error {
			environment.clock.Set(environment.clock.Now().Add(delay))
			return ctx.Err()
		},
		Transport: statusRoundTrip(func(request *http.Request) (*http.Response, error) {
			portalRequests = append(portalRequests, request.Method+" "+request.URL.Path)
			if request.Method == http.MethodGet {
				return statusHTTPResponse(request, string(bootstrap), http.StatusOK), nil
			}
			index := len(portalRequests) - 2
			if index < 0 || index >= len(rpcReplies) {
				return nil, fmt.Errorf("unexpected portal request")
			}
			return statusHTTPResponse(request, rpcReplies[index], http.StatusOK), nil
		}),
	})
	test.Cleanup(portalClient.Close)
	service := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
		Store: environment.store, Portal: portalClient, Clock: environment.clock,
	})
	test.Cleanup(service.Shutdown)
	telegramServer := newServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/sendRichMessage") {
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				test.Errorf("parse rich message request: %v", err)
			}
			var payload struct {
				HTML string `json:"html"`
			}
			if err := json.Unmarshal([]byte(request.FormValue("rich_message")), &payload); err != nil {
				test.Errorf("decode rich message: %v", err)
			}
			environment.richCalls++
			environment.richMessages = append(environment.richMessages, payload.HTML)
		} else if strings.HasSuffix(request.URL.Path, "/sendMessage") {
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				test.Errorf("parse message request: %v", err)
			}
			environment.replies = append(environment.replies, request.FormValue("text"))
		} else {
			test.Errorf("unexpected Telegram method %s", request.URL.Path)
			responseWriter.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = responseWriter.Write([]byte(`{"ok":true,"result":{"message_id":42,"chat":{"id":101,"type":"private"},"date":1}}`))
	}))
	server := telegramServer
	environment.bot = environment.newBot(test, server, Config{TrackedCaseService: service, Clock: environment.clock})

	// When
	environment.message(owner, "/status")
	trackedCase, readError := environment.store.TrackedCases().FindByOwner(ctx, owner)

	// Then
	assert.NoError(test, readError)
	assert.Equal(test, "Example stage", trackedCase.Stage)
	assert.True(test, strings.Contains(trackedCase.HTML, "<table>"), "the real portal renderer should persist the informational table")
	assert.True(test, trackedCase.CheckedAt.After(checkedAt), "the successful observation should replace the old timestamp")
	assert.Equal(test, 5, len(portalRequests), "the login bootstrap and four RPC exchanges should run through the portal client")
	assert.Equal(test, 1, environment.richCalls, "one rich status should be delivered")
	assert.Equal(test, 0, len(environment.replies), "rich status should not also send a classic text reply")
	message := environment.richMessages[0]
	for _, expected := range []string{
		"Name and surname", "Example Applicant",
		"Case number", "EXAMPLE-0001",
		"Date of filing the application", "2020-01-01",
		"Stage of your case", "Example stage", "Example description",
		"Notes", "Documents", "Incoming documents on 2020-01-01",
		"Последняя проверка:", "Данные успешно обновлены.",
	} {
		assert.True(test, strings.Contains(message, expected), "rich status should include the portal structure and metadata")
	}
	assert.True(test, strings.Contains(trackedCase.HTML, "<td><strong>Notes</strong></td><td></td>"), "the source snapshot should preserve the intentionally empty Notes value")
	assert.True(test, strings.Contains(trackedCase.HTML, "<em>Example description</em>"), "the source snapshot should preserve nested formatting")
	assert.True(test, !strings.Contains(message, "synthetic-password"), "rich status must not expose credentials")
	assert.True(test, !strings.Contains(message, "example-security-key"), "rich status must not expose portal tokens")

	// Restart the persistence/service/bot path and verify cached content without a portal request.
	service.Shutdown()
	portalClient.Close()
	assert.NoError(test, environment.db.Close())
	db, err := database.Open(ctx, environment.path)
	assert.NoError(test, err)
	environment.db = db
	environment.store = repositories.NewStore(db)
	test.Cleanup(func() { _ = db.Close() })
	failingPortal := &statusFailingPortal{}
	restartedService := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
		Store: environment.store, Portal: failingPortal, Clock: environment.clock,
	})
	test.Cleanup(restartedService.Shutdown)
	environment.bot = environment.newBot(test, server, Config{TrackedCaseService: restartedService, Clock: environment.clock})

	// When
	environment.message(owner, "/status")
	restartedMessage := environment.richMessages[len(environment.richMessages)-1]

	// Then
	assert.Equal(test, 2, environment.richCalls, "restarted service should deliver the persisted observation")
	assert.Equal(test, 0, failingPortal.calls, "recent persisted observation should avoid a portal refresh")
	assert.True(test, strings.Contains(restartedMessage, "Последняя успешная проверка:"), "restarted status should be labeled as cached")
	assert.True(test, strings.Contains(restartedMessage, "Example Applicant"), "restarted status should retain the persisted portal content")
	assert.True(test, !strings.Contains(restartedMessage, "Данные успешно обновлены."), "cached response should not claim a fresh update")

	// Given
	previousObservation := trackedCase
	environment.clock.Set(environment.clock.Now().Add(2 * time.Minute))

	// When
	environment.message(owner, "/status")
	failedRefreshMessage := environment.richMessages[len(environment.richMessages)-1]
	storedAfterFailure, readError := environment.store.TrackedCases().FindByOwner(ctx, owner)

	// Then
	assert.NoError(test, readError)
	assert.Equal(test, 1, failingPortal.calls, "eligible status should attempt one portal refresh")
	assert.True(test, strings.Contains(failedRefreshMessage, "Не удалось обновить статус дела."), "failed refresh should explain that cached content is shown")
	assert.True(test, strings.Contains(failedRefreshMessage, "Показываю данные последней успешной проверки:"), "failed refresh should identify the prior observation")
	assert.True(test, strings.Contains(failedRefreshMessage, "Example Applicant"), "failed refresh should keep the previous portal content visible")
	assert.True(test, strings.Contains(failedRefreshMessage, "Проверено:"), "failed refresh should use the original check timestamp label")
	assert.True(test, !strings.Contains(failedRefreshMessage, "Данные успешно обновлены."), "failed refresh must not claim success")
	assert.Equal(test, previousObservation.HTML, storedAfterFailure.HTML, "failed refresh should leave the committed HTML unchanged")
	assert.True(test, previousObservation.CheckedAt.Equal(storedAfterFailure.CheckedAt), "failed refresh should leave the committed timestamp unchanged")

	// Given
	environment.clock.Set(environment.clock.Now().Add(2 * time.Minute))
	stageOnlyPortal := &statusStageOnlyPortal{clock: environment.clock}
	stageOnlyService := services.NewTrackedCaseService(services.TrackedCaseServiceConfig{
		Store: environment.store, Portal: stageOnlyPortal, Clock: environment.clock,
	})
	test.Cleanup(stageOnlyService.Shutdown)
	environment.bot = environment.newBot(test, server, Config{TrackedCaseService: stageOnlyService, Clock: environment.clock})

	// When
	environment.message(owner, "/status")
	stageOnlyReply := environment.replies[len(environment.replies)-1]
	storedStageOnly, readError := environment.store.TrackedCases().FindByOwner(ctx, owner)

	// Then
	assert.NoError(test, readError)
	assert.Equal(test, 3, environment.richCalls, "stage-only status should not create another rich message")
	assert.Equal(test, "New stage", storedStageOnly.Stage)
	assert.Equal(test, "", storedStageOnly.HTML, "a successful stage-only observation should clear old portal content")
	assert.True(test, strings.Contains(stageOnlyReply, "Статус дела: New stage"), "stage-only response should retain legacy status formatting")
	assert.True(test, !strings.Contains(stageOnlyReply, "Example Applicant"), "stage-only response must not reuse stale portal content")
}

type statusRoundTrip func(*http.Request) (*http.Response, error)

func (this statusRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}

func statusHTTPResponse(request *http.Request, body string, status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func readStatusFixture(test *testing.T, path string) []byte {
	test.Helper()
	data, err := os.ReadFile(path)
	assert.NoError(test, err)
	return data
}

type statusFailingPortal struct {
	calls int
}

func (this *statusFailingPortal) Check(context.Context, int64, string, string) (domain.PortalSnapshot, error) {
	this.calls++
	return domain.PortalSnapshot{}, errors.New("synthetic offline portal failure")
}

func (this *statusFailingPortal) ForgetSession(int64) {}

type statusStageOnlyPortal struct {
	clock domain.Clock
}

func (this *statusStageOnlyPortal) Check(context.Context, int64, string, string) (domain.PortalSnapshot, error) {
	return domain.PortalSnapshot{Stage: "New stage", CheckedAt: this.clock.Now()}, nil
}

func (this *statusStageOnlyPortal) ForgetSession(int64) {}
