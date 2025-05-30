package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pkg/errors"
)

// The current Azure rate limit is 1000 posts per minute.
// Using half of that to give us some wiggle room:
// https://learn.microsoft.com/en-us/azure/ai-services/content-safety/faq
const (
	postsPerMinuteLimit = 500
	processingInterval  = 1 / postsPerMinuteLimit * time.Minute
)

// Message templates for moderation notifications
const (
	channelNotificationTemplate = "_A post with potentially offensive content was flagged and removed._"
	dmNotificationTemplate      = "_Your post with the following content was flagged and removed:_\n\n%s"
)

var (
	ErrModerationRejection   = errors.New("potentially inappropriate content detected")
	ErrModerationUnavailable = errors.New("moderation service is not available")
)

type PostProcessor struct {
	botID     string
	moderator moderation.Moderator

	stopChan chan bool

	thresholdValue int
	excludedUsers  map[string]struct{}
	excludedGroups map[string]struct{}

	postsToProcess []*model.Post
	processLock    sync.Mutex
}

func newPostProcessor(
	botID string,
	moderator moderation.Moderator,
	thresholdValue int,
	excludedUsers map[string]struct{},
	excludedGroups map[string]struct{},
) (*PostProcessor, error) {
	if moderator == nil {
		return nil, ErrModerationUnavailable
	}
	return &PostProcessor{
		botID:          botID,
		moderator:      moderator,
		stopChan:       make(chan bool, 1),
		thresholdValue: thresholdValue,
		excludedUsers:  excludedUsers,
		excludedGroups: excludedGroups,
	}, nil
}

func (p *PostProcessor) start(api plugin.API) {
	go func() {
		for {
			select {
			case <-p.stopChan:
				return
			default:
			}

			time.Sleep(processingInterval)

			post := p.popPostForProcessing()
			if post == nil || post.Message == "" {
				continue
			}

			shouldModerate, err := p.shouldModerateUser(api, post.UserId)
			if err != nil {
				api.LogError("Could not determine if user should be moderated", "err", err, "post_id", post.Id, "user_id", post.UserId)
				continue
			}
			if !shouldModerate {
				continue
			}

			if err := p.moderatePost(api, post); err != nil {
				continue
			}

			if errors.Is(err, ErrModerationUnavailable) {
				api.LogError("Content moderation error", "err", err, "post_id", post.Id, "user_id", post.UserId)
				continue
			}

			if err := api.DeletePost(post.Id); err != nil {
				api.LogError("Failed to delete post flagged by content moderation", "post_id", post.Id, "err", err)
			}

			if err := p.reportModerationEvent(api, post); err != nil {
				api.LogError("Failed report content moderation event", "post_id", post.Id, "err", err)
			}
		}
	}()
}

func (p *PostProcessor) stop() {
	p.stopChan <- true
}

func (p *PostProcessor) queuePostForProcessing(post *model.Post) {
	p.processLock.Lock()
	defer p.processLock.Unlock()

	p.postsToProcess = append(p.postsToProcess, post)
}

func (p *PostProcessor) popPostForProcessing() *model.Post {
	p.processLock.Lock()
	defer p.processLock.Unlock()

	if len(p.postsToProcess) == 0 {
		return nil
	}

	post := p.postsToProcess[0]
	p.postsToProcess = p.postsToProcess[1:]

	return post
}

func (p *PostProcessor) moderatePost(api plugin.API, post *model.Post) error {
	ctx, cancel := context.WithTimeout(context.Background(), moderationTimeout)
	defer cancel()

	result, err := p.moderator.ModerateText(ctx, post.Message)
	if err != nil {
		return ErrModerationUnavailable
	}

	if p.resultSeverityAboveThreshold(result) {
		p.logFlaggedResult(api, post.UserId, result)
		return ErrModerationRejection
	}

	return nil
}

func (p *PostProcessor) shouldModerateUser(api plugin.API, userID string) (bool, error) {
	if userID == p.botID {
		return false, nil
	}
	if len(p.excludedUsers) == 0 {
		return true, nil
	}
	if _, excluded := p.excludedUsers[userID]; excluded {
		return false, nil
	}
	groups, err := api.GetGroupsForUser(userID)
	if err != nil {
		return false, err
	}
	for _, group := range groups {
		if _, excluded := p.excludedGroups[group.Id]; excluded {
			return false, nil
		}
	}
	return true, nil
}

func (p *PostProcessor) resultSeverityAboveThreshold(result moderation.Result) bool {
	for _, severity := range result {
		if severity >= p.thresholdValue {
			return true
		}
	}
	return false
}

func (p *PostProcessor) logFlaggedResult(api plugin.API, userID string, result moderation.Result) {
	keyPairs := []any{"user_id", userID, "threshold", p.thresholdValue}

	for category, severity := range result {
		if severity >= p.thresholdValue {
			keyPairs = append(keyPairs, category)
			keyPairs = append(keyPairs, severity)
		}
	}

	api.LogInfo("Content was flagged by moderation", keyPairs...)
}

func (p *PostProcessor) reportModerationEvent(api plugin.API, post *model.Post) error {
	if _, err := api.CreatePost(&model.Post{
		UserId:    p.botID,
		ChannelId: post.ChannelId,
		RootId:    post.RootId,
		Message:   channelNotificationTemplate,
	}); err != nil {
		return errors.Wrap(err, "failed to post channel notification")
	}

	dmChannel, err := api.GetDirectChannel(p.botID, post.UserId)
	if err != nil {
		return errors.Wrap(err, "failed to create DM channel")
	}

	if _, err := api.CreatePost(&model.Post{
		UserId:    p.botID,
		ChannelId: dmChannel.Id,
		Message:   fmt.Sprintf(dmNotificationTemplate, post.Message),
	}); err != nil {
		return errors.Wrap(err, "failed to send DM notification")
	}

	return nil
}
