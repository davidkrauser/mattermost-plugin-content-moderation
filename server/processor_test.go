package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockModerator is a mock implementation of the Moderator interface
type MockModerator struct {
	mock.Mock
}

func (m *MockModerator) ModerateText(ctx context.Context, text string) (moderation.Result, error) {
	args := m.Called(ctx, text)
	return args.Get(0).(moderation.Result), args.Error(1)
}

func TestResultSeverityAboveThreshold(t *testing.T) {
	tests := []struct {
		name           string
		result         moderation.Result
		thresholdValue int
		expected       bool
	}{
		{
			name: "All severities below threshold",
			result: moderation.Result{
				"hate":     20,
				"sexual":   15,
				"violence": 30,
			},
			thresholdValue: 50,
			expected:       false,
		},
		{
			name: "One severity above threshold",
			result: moderation.Result{
				"hate":     20,
				"sexual":   75,
				"violence": 30,
			},
			thresholdValue: 50,
			expected:       true,
		},
		{
			name: "Multiple severities above threshold",
			result: moderation.Result{
				"hate":     60,
				"sexual":   75,
				"violence": 80,
			},
			thresholdValue: 50,
			expected:       true,
		},
		{
			name: "Severity equal to threshold",
			result: moderation.Result{
				"hate":     20,
				"sexual":   50,
				"violence": 30,
			},
			thresholdValue: 50,
			expected:       true,
		},
		{
			name:           "Empty result",
			result:         moderation.Result{},
			thresholdValue: 50,
			expected:       false,
		},
		{
			name: "Zero threshold",
			result: moderation.Result{
				"hate": 1,
			},
			thresholdValue: 0,
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &PostProcessor{
				thresholdValue: tt.thresholdValue,
			}

			result := processor.resultSeverityAboveThreshold(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestQueuePostForProcessing(t *testing.T) {
	t.Run("Queue post successfully", func(t *testing.T) {
		processor := &PostProcessor{
			postsCh: make(chan *model.Post, 10),
		}
		api := &plugintest.API{}
		post := &model.Post{Id: "post1", Message: "Test message"}

		processor.queuePostForProcessing(api, post)

		// Verify post is in channel
		select {
		case queuedPost := <-processor.postsCh:
			assert.Equal(t, post, queuedPost)
		default:
			t.Fatal("Post was not queued")
		}
	})

	t.Run("Queue full - log error", func(t *testing.T) {
		processor := &PostProcessor{
			postsCh: make(chan *model.Post, 1), // Small buffer
		}

		api := &plugintest.API{}
		api.On("LogError", "Content moderation unable to analyze post: exceeded maximum post queue size", "post_id", "post2").Return()

		post1 := &model.Post{Id: "post1", Message: "First message"}
		post2 := &model.Post{Id: "post2", Message: "Second message"}

		// Fill the channel
		processor.queuePostForProcessing(api, post1)

		// This should fail and log error
		processor.queuePostForProcessing(api, post2)

		// Verify first post is still there
		select {
		case queuedPost := <-processor.postsCh:
			assert.Equal(t, post1, queuedPost)
		default:
			t.Fatal("First post should still be in queue")
		}

		api.AssertExpectations(t)
	})

	t.Run("Queue post after shutdown", func(t *testing.T) {
		processor := &PostProcessor{
			postsCh: make(chan *model.Post, 10),
		}

		api := &plugintest.API{}
		api.On("LogDebug", "Panic occurred while queueing post for processing",
			"post_id", "post1", "panic", mock.MatchedBy(func(v interface{}) bool {
				return strings.Contains(fmt.Sprintf("%v", v), "send on closed channel")
			})).Return()

		post := &model.Post{Id: "post1", Message: "Test message"}

		// Close the channel to simulate shutdown
		close(processor.postsCh)

		// This should not panic even with closed channel
		processor.queuePostForProcessing(api, post)

		// Verify no post was queued (channel is closed)
		select {
		case _, ok := <-processor.postsCh:
			if ok {
				t.Fatal("Should not be able to receive from closed channel")
			}
		default:
			// Expected - channel is closed and empty
		}

		api.AssertExpectations(t)
	})
}

func TestShouldModerateUser(t *testing.T) {
	tests := []struct {
		name          string
		excludedUsers map[string]struct{}
		userID        string
		expected      bool
	}{
		{
			name:          "User not excluded",
			excludedUsers: map[string]struct{}{},
			userID:        "any_user",
			expected:      true,
		},
		{
			name:          "User is excluded",
			excludedUsers: map[string]struct{}{"user1": {}, "user2": {}},
			userID:        "user1",
			expected:      false,
		},
		{
			name:          "User not in excluded list",
			excludedUsers: map[string]struct{}{"user1": {}, "user2": {}},
			userID:        "user3",
			expected:      true,
		},
		{
			name:          "Empty excluded list",
			excludedUsers: map[string]struct{}{},
			userID:        "any_user",
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &PostProcessor{
				excludedUsers: tt.excludedUsers,
			}

			result := processor.shouldModerateUser(tt.userID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldModerateChannel(t *testing.T) {
	tests := []struct {
		name             string
		excludedChannels map[string]struct{}
		channelID        string
		expected         bool
	}{
		{
			name:             "Channel not excluded",
			excludedChannels: map[string]struct{}{},
			channelID:        "any_channel",
			expected:         true,
		},
		{
			name:             "Channel is excluded",
			excludedChannels: map[string]struct{}{"channel1": {}, "channel2": {}},
			channelID:        "channel1",
			expected:         false,
		},
		{
			name:             "Channel not in excluded list",
			excludedChannels: map[string]struct{}{"channel1": {}, "channel2": {}},
			channelID:        "channel3",
			expected:         true,
		},
		{
			name:             "Empty excluded list",
			excludedChannels: map[string]struct{}{},
			channelID:        "any_channel",
			expected:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &PostProcessor{
				excludedChannels: tt.excludedChannels,
			}

			result := processor.shouldModerateChannel(tt.channelID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Only test the simple cases to avoid race conditions in the test
func TestModeratePost(t *testing.T) {
	t.Run("Skip moderation for excluded user", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockModerator := &MockModerator{}

		processor := &PostProcessor{
			moderator:     mockModerator,
			excludedUsers: map[string]struct{}{"user1": {}, "user2": {}},
		}

		post := &model.Post{UserId: "user1", Message: "Test message"}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.NoError(t, err)
		mockModerator.AssertNotCalled(t, "ModerateText")
	})

	t.Run("Skip moderation for excluded channel", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockModerator := &MockModerator{}

		processor := &PostProcessor{
			moderator:        mockModerator,
			excludedUsers:    map[string]struct{}{},
			excludedChannels: map[string]struct{}{"channel1": {}, "channel2": {}},
		}

		post := &model.Post{UserId: "user1", ChannelId: "channel1", Message: "Test message"}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.NoError(t, err)
		mockModerator.AssertNotCalled(t, "ModerateText")
	})

	t.Run("Skip moderation for empty message", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockModerator := &MockModerator{}

		processor := &PostProcessor{
			moderator:     mockModerator,
			excludedUsers: map[string]struct{}{},
		}

		post := &model.Post{UserId: "user1", Message: ""}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.NoError(t, err)
		mockModerator.AssertNotCalled(t, "ModerateText")
	})

	t.Run("Moderation API failure", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Return()

		mockModerator := &MockModerator{}
		mockModerator.On("ModerateText", mock.Anything, "Test message").
			Return(moderation.Result{}, errors.New("API error"))

		processor := &PostProcessor{
			moderator:      mockModerator,
			excludedUsers:  map[string]struct{}{},
			thresholdValue: 50,
		}

		post := &model.Post{UserId: "user1", Message: "Test message"}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.Equal(t, ErrModerationUnavailable, err)
		mockModerator.AssertExpectations(t)
	})

	t.Run("Content below threshold", func(t *testing.T) {
		mockAPI := &plugintest.API{}

		mockModerator := &MockModerator{}
		mockModerator.On("ModerateText", mock.Anything, "Test message").
			Return(moderation.Result{"category": 10}, nil)

		processor := &PostProcessor{
			moderator:      mockModerator,
			excludedUsers:  map[string]struct{}{},
			thresholdValue: 50,
		}

		post := &model.Post{UserId: "user1", Message: "Test message"}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.NoError(t, err)
		mockModerator.AssertExpectations(t)
	})

	t.Run("Content above threshold", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("LogInfo", "Content was flagged by moderation",
			"post_id", "", "severity_threshold", 50, "computed_severity_sexual", 80).Return()

		mockModerator := &MockModerator{}
		mockModerator.On("ModerateText", mock.Anything, "Inappropriate content").
			Return(moderation.Result{
				"hate":     10,
				"sexual":   80, // Above threshold
				"violence": 30,
			}, nil)

		processor := &PostProcessor{
			moderator:      mockModerator,
			excludedUsers:  map[string]struct{}{},
			thresholdValue: 50,
		}

		post := &model.Post{UserId: "user1", Message: "Inappropriate content"}
		auditRecord := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
		err := processor.moderatePost(mockAPI, post, auditRecord)

		assert.Equal(t, ErrModerationRejection, err) // Should return rejection error
		mockModerator.AssertExpectations(t)
		mockAPI.AssertExpectations(t)
	})
}

func TestNewPostProcessor(t *testing.T) {
	tests := []struct {
		name             string
		botID            string
		moderator        moderation.Moderator
		thresholdValue   int
		excludedUsers    map[string]struct{}
		excludedChannels map[string]struct{}
		wantErr          bool
		expectedErr      error
	}{
		{
			name:             "Valid moderator with thresholds",
			botID:            "bot123",
			moderator:        &MockModerator{},
			thresholdValue:   75,
			excludedUsers:    map[string]struct{}{"user1": {}},
			excludedChannels: map[string]struct{}{"channel1": {}},
			wantErr:          false,
		},
		{
			name:             "Nil moderator",
			botID:            "bot123",
			moderator:        nil,
			thresholdValue:   75,
			excludedUsers:    map[string]struct{}{"user1": {}},
			excludedChannels: map[string]struct{}{"channel1": {}},
			wantErr:          true,
			expectedErr:      ErrModerationUnavailable,
		},
		{
			name:             "Zero threshold",
			botID:            "bot123",
			moderator:        &MockModerator{},
			thresholdValue:   0,
			excludedUsers:    map[string]struct{}{"user1": {}},
			excludedChannels: map[string]struct{}{"channel1": {}},
			wantErr:          false,
		},
		{
			name:             "No excluded users",
			botID:            "bot123",
			moderator:        &MockModerator{},
			thresholdValue:   75,
			excludedUsers:    map[string]struct{}{},
			excludedChannels: map[string]struct{}{"channel1": {}},
			wantErr:          false,
		},
		{
			name:             "No excluded channels",
			botID:            "bot123",
			moderator:        &MockModerator{},
			thresholdValue:   75,
			excludedUsers:    map[string]struct{}{"user1": {}},
			excludedChannels: map[string]struct{}{},
			wantErr:          false,
		},
		{
			name:             "No excluded users or channels",
			botID:            "bot123",
			moderator:        &MockModerator{},
			thresholdValue:   75,
			excludedUsers:    map[string]struct{}{},
			excludedChannels: map[string]struct{}{},
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := newPostProcessor(tt.botID, tt.moderator, false, tt.thresholdValue, tt.excludedUsers, tt.excludedChannels)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.expectedErr != nil {
					assert.Equal(t, tt.expectedErr, err)
				}
				assert.Nil(t, processor)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, processor)
				assert.Equal(t, tt.botID, processor.botID)
				assert.Equal(t, tt.moderator, processor.moderator)
				assert.Equal(t, tt.thresholdValue, processor.thresholdValue)
				assert.Equal(t, tt.excludedUsers, processor.excludedUsers)
				assert.Equal(t, tt.excludedChannels, processor.excludedChannels)
				assert.NotNil(t, processor.postsCh)
			}
		})
	}
}
