package sqlstore

import (
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/pkg/errors"
)

var escapeLikeSearchChar = []string{
	"%",
	"_",
}

func sanitizeSearchTerm(term string) string {
	const escapeChar = "\\"

	term = strings.ReplaceAll(term, escapeChar, "")

	for _, c := range escapeLikeSearchChar {
		term = strings.ReplaceAll(term, c, escapeChar+c)
	}

	return term
}

func (ss SQLStore) SearchChannelsByPrefix(prefix string) ([]*model.Channel, error) {
	sanitizedPrefix := strings.ToLower(sanitizeSearchTerm(prefix))
	query := ss.replicaBuilder.
		Select("Id", "Name", "DisplayName", "Type", "DeleteAt").
		From("Channels").
		Where(sq.Or{
			sq.Like{"LOWER(DisplayName)": sanitizedPrefix + "%"},
			sq.Like{"LOWER(Name)": sanitizedPrefix + "%"},
		}).
		Where(sq.Eq{"DeleteAt": 0}).
		Where(sq.Or{
			sq.Eq{"Type": string(model.ChannelTypeOpen)},
			sq.Eq{"Type": string(model.ChannelTypePrivate)},
		}).
		OrderBy("DisplayName").
		Limit(20)

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build SQL query for searching channels")
	}

	var channels []*model.Channel
	if err := ss.replica.Select(&channels, sql, args...); err != nil {
		return nil, errors.Wrap(err, "failed to search channels")
	}

	return channels, nil
}
