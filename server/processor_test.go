package main

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation"
	"github.com/mattermost/mattermost/server/public/model"
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

func TestQueueAndPopPost(t *testing.T) {
	t.Run("Queue and pop single post", func(t *testing.T) {
		processor := &PostProcessor{}
		post := &model.Post{Id: "post1", Message: "Test message"}

		// Queue a post
		processor.queuePostForProcessing(&plugintest.API{}, post)

		// Verify it's in the queue
		assert.Len(t, processor.postsToProcess, 1)
		assert.Equal(t, post, processor.postsToProcess[0])

		// Pop the post and verify it's the same
		poppedPost := processor.popPostForProcessing()
		assert.Equal(t, post, poppedPost)

		// Verify queue is now empty
		assert.Empty(t, processor.postsToProcess)

		// Verify popping from empty queue returns nil
		emptyPost := processor.popPostForProcessing()
		assert.Nil(t, emptyPost)
	})

	t.Run("Queue and pop multiple posts in FIFO order", func(t *testing.T) {
		processor := &PostProcessor{}
		post1 := &model.Post{Id: "post1", Message: "First message"}
		post2 := &model.Post{Id: "post2", Message: "Second message"}
		post3 := &model.Post{Id: "post3", Message: "Third message"}

		// Queue posts
		processor.queuePostForProcessing(&plugintest.API{}, post1)
		processor.queuePostForProcessing(&plugintest.API{}, post2)
		processor.queuePostForProcessing(&plugintest.API{}, post3)

		// Verify queue length
		assert.Len(t, processor.postsToProcess, 3)

		// Verify posts are popped in FIFO order (first in, first out)
		assert.Equal(t, post1, processor.popPostForProcessing())
		assert.Equal(t, post2, processor.popPostForProcessing())
		assert.Equal(t, post3, processor.popPostForProcessing())

		// Verify queue is now empty
		assert.Empty(t, processor.postsToProcess)
	})

	t.Run("Thread safety of queue operations", func(t *testing.T) {
		processor := &PostProcessor{}
		const numPosts = 20
		var wg sync.WaitGroup

		// Create a bunch of posts
		posts := make([]*model.Post, numPosts)
		for i := 0; i < numPosts; i++ {
			posts[i] = &model.Post{Id: fmt.Sprintf("post%d", i), Message: fmt.Sprintf("Message %d", i)}
		}

		// Queue them from multiple goroutines
		wg.Add(numPosts)
		for i := 0; i < numPosts; i++ {
			go func(idx int) {
				defer wg.Done()
				processor.queuePostForProcessing(&plugintest.API{}, posts[idx])
			}(i)
		}
		wg.Wait()

		// Verify all posts were queued
		assert.Len(t, processor.postsToProcess, numPosts)

		// Pop them from multiple goroutines
		poppedPosts := make([]*model.Post, 0, numPosts)
		var popMutex sync.Mutex

		wg.Add(numPosts)
		for i := 0; i < numPosts; i++ {
			go func() {
				defer wg.Done()
				post := processor.popPostForProcessing()
				if post != nil {
					popMutex.Lock()
					poppedPosts = append(poppedPosts, post)
					popMutex.Unlock()
				}
			}()
		}
		wg.Wait()

		// Verify we got all posts back (though not necessarily in order due to concurrency)
		assert.Len(t, poppedPosts, numPosts)
		assert.Empty(t, processor.postsToProcess)
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
		err := processor.moderatePost(mockAPI, post)

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
		err := processor.moderatePost(mockAPI, post)

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
		err := processor.moderatePost(mockAPI, post)

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
		err := processor.moderatePost(mockAPI, post)

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
		err := processor.moderatePost(mockAPI, post)

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
		err := processor.moderatePost(mockAPI, post)

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
			processor, err := newPostProcessor(tt.botID, tt.moderator, tt.thresholdValue, tt.excludedUsers, tt.excludedChannels)

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
				assert.NotNil(t, processor.stopChan)
				assert.Empty(t, processor.postsToProcess)
			}
		})
	}
}
