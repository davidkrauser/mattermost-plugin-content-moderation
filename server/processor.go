package main

import (
	"context"
	"fmt"
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
	maxProcessingQueueSize = 10000
	postsPerMinuteLimit    = 500
	processingInterval     = 1 / postsPerMinuteLimit * time.Minute
)

// Message templates for moderation notifications
const (
	channelNotificationTemplate = "_A post with potentially offensive content was flagged and removed._"
	dmNotificationTemplate      = "_Your post with the following content was flagged and removed:_\n\n%s"
)

// Audit event constants
const (
	auditEventTypeContentModeration = "contentModeration"
	auditMetaKeyFlagged             = "flagged"
	auditMetaKeyResult              = "result"
	auditMetaKeyThreshold           = "threshold"
	auditMetaKeyExcluded            = "excluded"
	auditMetaKeyPost                = "post"
)

var (
	ErrModerationRejection   = errors.New("potentially inappropriate content detected")
	ErrModerationUnavailable = errors.New("moderation service is not available")
)

type PostProcessor struct {
	botID            string
	moderator        moderation.Moderator
	auditLogEnabled  bool
	thresholdValue   int
	excludedUsers    map[string]struct{}
	excludedChannels map[string]struct{}
	postsCh          chan *model.Post
}

func newPostProcessor(
	botID string,
	moderator moderation.Moderator,
	auditLogEnabled bool,
	thresholdValue int,
	excludedUsers map[string]struct{},
	excludedChannels map[string]struct{},
) (*PostProcessor, error) {
	if moderator == nil {
		return nil, ErrModerationUnavailable
	}
	return &PostProcessor{
		botID:            botID,
		moderator:        moderator,
		auditLogEnabled:  auditLogEnabled,
		thresholdValue:   thresholdValue,
		excludedUsers:    excludedUsers,
		excludedChannels: excludedChannels,
		postsCh:          make(chan *model.Post, maxProcessingQueueSize),
	}, nil
}

func (p *PostProcessor) start(api plugin.API) {
	go func() {
		for {
			post, ok := <-p.postsCh
			if !ok {
				return
			}

			time.Sleep(processingInterval)

			record := plugin.MakeAuditRecord(auditEventTypeContentModeration, model.AuditStatusAttempt)
			model.AddEventParameterAuditableToAuditRec(record, auditMetaKeyPost, post)

			err := p.moderatePost(api, post, record)
			if err == nil {
				p.logAuditSuccess(api, record)
				continue
			}

			if errors.Is(err, ErrModerationUnavailable) {
				errMsg := "Content moderation error"
				api.LogError(errMsg, "err", err, "post_id", post.Id, "user_id", post.UserId)
				p.logAuditFail(api, record, errMsg, err)
				continue
			}

			if err := api.DeletePost(post.Id); err != nil {
				errMsg := "Failed to delete post flagged by content moderation"
				api.LogError(errMsg, "post_id", post.Id, "err", err)
				p.logAuditFail(api, record, errMsg, err)
				continue
			}

			if err := p.reportModerationEvent(api, post); err != nil {
				errMsg := "Failed report content moderation event"
				api.LogError(errMsg, "post_id", post.Id, "err", err)
				p.logAuditFail(api, record, errMsg, err)
				continue
			}

			p.logAuditSuccess(api, record)
		}
	}()
}

func (p *PostProcessor) stop() {
	close(p.postsCh)
}

func (p *PostProcessor) queuePostForProcessing(api plugin.API, post *model.Post) {
	defer func() {
		if r := recover(); r != nil {
			api.LogDebug("Panic occurred while queueing post for processing", "post_id", post.Id, "panic", r)
		}
	}()

	select {
	case p.postsCh <- post:
	default:
		api.LogError("Content moderation unable to analyze post: exceeded maximum post queue size", "post_id", post.Id)
	}
}

func (p *PostProcessor) moderatePost(api plugin.API, post *model.Post, auditRecord *model.AuditRecord) error {
	if post.Message == "" {
		return nil
	}

	if !p.shouldModerateUser(post.UserId) {
		auditRecord.AddMeta(auditMetaKeyExcluded, true)
		return nil
	}

	if !p.shouldModerateChannel(post.ChannelId) {
		auditRecord.AddMeta(auditMetaKeyExcluded, true)
		return nil
	}

	auditRecord.AddMeta(auditMetaKeyExcluded, false)

	ctx, cancel := context.WithTimeout(context.Background(), moderationTimeout)
	defer cancel()

	result, err := p.moderator.ModerateText(ctx, post.Message)
	if err != nil {
		return ErrModerationUnavailable
	}

	auditRecord.AddMeta(auditMetaKeyThreshold, p.thresholdValue)
	auditRecord.AddMeta(auditMetaKeyResult, result)

	if p.resultSeverityAboveThreshold(result) {
		auditRecord.AddMeta(auditMetaKeyFlagged, true)
		p.logFlaggedResult(api, post.Id, result)
		return ErrModerationRejection
	}

	auditRecord.AddMeta(auditMetaKeyFlagged, false)
	return nil
}

func (p *PostProcessor) shouldModerateUser(userID string) bool {
	if userID == p.botID {
		return false
	}
	if len(p.excludedUsers) == 0 {
		return true
	}
	_, excluded := p.excludedUsers[userID]
	return !excluded
}

func (p *PostProcessor) shouldModerateChannel(channelID string) bool {
	if len(p.excludedChannels) == 0 {
		return true
	}
	_, excluded := p.excludedChannels[channelID]
	return !excluded
}

func (p *PostProcessor) resultSeverityAboveThreshold(result moderation.Result) bool {
	for _, severity := range result {
		if severity >= p.thresholdValue {
			return true
		}
	}
	return false
}

func (p *PostProcessor) logFlaggedResult(api plugin.API, postID string, result moderation.Result) {
	keyPairs := []any{"post_id", postID, "severity_threshold", p.thresholdValue}

	for category, severity := range result {
		if severity >= p.thresholdValue {
			keyPairs = append(keyPairs, fmt.Sprintf("computed_severity_%s", category))
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

func (p *PostProcessor) logAuditSuccess(api plugin.API, auditRecord *model.AuditRecord) {
	if !p.auditLogEnabled {
		return
	}
	auditRecord.Success()
	api.LogAuditRec(auditRecord)
}

func (p *PostProcessor) logAuditFail(api plugin.API, auditRecord *model.AuditRecord, errDesc string, err error) {
	if !p.auditLogEnabled {
		return
	}
	auditRecord.Fail()
	auditRecord.AddErrorDesc(errors.Wrap(err, errDesc).Error())
	api.LogAuditRec(auditRecord)
}
