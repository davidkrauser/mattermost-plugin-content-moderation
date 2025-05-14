package main

import (
	"context"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-content-moderator/server/moderation"
	"github.com/mattermost/mattermost-plugin-content-moderator/server/moderation/azure"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const moderationTimeout = 10 * time.Second

var (
	ErrModerationRejection   = errors.New("_This post has been redacted by the moderation plugin: potentially inappropriate content detected_")
	ErrModerationUnavailable = errors.New("_This post has been redacted by the moderation plugin: moderation service is not available_")
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	client    *pluginapi.Client
	moderator moderation.Moderator

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex
	configuration     *configuration

	thresholdValue int
	targetUsers    map[string]struct{}
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	if err := p.initModerator(); err != nil {
		return errors.Wrap(err, "failed to initialize moderator")
	}

	return nil
}

// initModerator initializes the content moderation service based on configuration
func (p *Plugin) initModerator() error {
	config := p.getConfiguration()

	if !config.Enabled {
		p.API.LogInfo("Content moderation is disabled")
		p.moderator = nil
		return nil
	}

	// Create appropriate moderator based on type
	switch config.Type {
	case "azure":
		azureConfig := &moderation.Config{
			Endpoint: config.Endpoint,
			APIKey:   config.APIKey,
		}

		mod, err := azure.New(azureConfig)
		if err != nil {
			p.API.LogError("failed to create Azure moderator", "err", err)
			return errors.Wrap(err, "failed to create Azure moderator")
		}

		p.moderator = mod
		p.API.LogInfo("Azure AI Content Safety moderator initialized")

	default:
		return errors.Errorf("unknown moderator type: %s", config.Type)
	}

	thresholdValue, err := config.ThresholdValue()
	if err != nil {
		p.API.LogError("failed to load moderation threshold", "err", err)
		return errors.Wrap(err, "failed to load moderation threshold")
	}
	p.thresholdValue = thresholdValue

	p.targetUsers = config.ModerationTargetsList()

	return nil
}

// MessageWillBePosted is invoked when a message is posted by a user, before it is committed
// to the database. This allows the plugin to reject posts that don't meet the moderation criteria.
//
// To reject a post, return an non-empty string describing why the post was rejected. To modify the
// post, return the replacement, non-nil *model.Post and an empty string. To allow the post without
// modification, return a nil *model.Post and an empty string. To dismiss the post, return a nil
// *model.Post and the const DismissPostError string.
func (p *Plugin) MessageWillBePosted(c *plugin.Context, newPost *model.Post) (*model.Post, string) {
	if err := p.moderatePost(newPost); err != nil {
		newPost.Message = err.Error()
		return newPost, ""
	}
	return nil, ""
}

// MessageWillBeUpdated is invoked when a message is updated by a user, before it is committed
// to the database. This allows the plugin to reject post updates that don't meet moderation criteria.
//
// Return values should be the modified post or nil if rejected and an explanation for the user.
// On rejection, the post will be kept in its previous state.
func (p *Plugin) MessageWillBeUpdated(c *plugin.Context, newPost, oldPost *model.Post) (*model.Post, string) {
	if err := p.moderatePost(newPost); err != nil {
		newPost.Message = err.Error()
		return newPost, ""
	}
	return newPost, ""
}

// moderatePost is the main entry point for content moderation of posts
func (p *Plugin) moderatePost(post *model.Post) error {
	// Skip moderation if not enabled or if user is excluded
	if p.moderator == nil || !p.shouldModerateUser(post.UserId) {
		return nil
	}

	// Skip empty messages
	if post.Message == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), moderationTimeout)
	defer cancel()

	result, err := p.moderator.ModerateText(ctx, post.Message)
	if err != nil {
		p.API.LogError("Text moderation failed", "err", err)
		return ErrModerationUnavailable
	}

	// Check if the content violates the configured thresholds
	if p.resultSeverityAboveThreshold(result) {
		p.logFlaggedResult(post.UserId, result)
		return ErrModerationRejection
	}

	return nil
}

// shouldModerateUser determines if the given user's content should be moderated
func (p *Plugin) shouldModerateUser(userID string) bool {
	config := p.getConfiguration()

	// If moderation is applied to all users, no need to check specific targets
	if config.ModerateAllUsers {
		return true
	}

	// Check if the user is in the targets map
	_, exists := p.targetUsers[userID]
	return exists
}

func (p *Plugin) resultSeverityAboveThreshold(result moderation.Result) bool {
	for _, severity := range result {
		if severity >= p.thresholdValue {
			return true
		}
	}

	return false
}

func (p *Plugin) logFlaggedResult(userID string, result moderation.Result) {
	keyPairs := []any{"user_id", userID, "threshold", p.thresholdValue}

	for category, severity := range result {
		if severity >= p.thresholdValue {
			keyPairs = append(keyPairs, category)
			keyPairs = append(keyPairs, severity)
		}
	}

	p.API.LogInfo("Content was flagged by moderation", keyPairs...)
}
