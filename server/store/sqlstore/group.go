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

func (ss SQLStore) SearchLDAPGroupsByPrefix(prefix string) ([]*model.Group, error) {
	sanitizedPrefix := strings.ToLower(sanitizeSearchTerm(prefix))
	query := ss.replicaBuilder.
		Select("Id", "Name", "DisplayName", "DeleteAt").
		From("UserGroups").
		Where(sq.Or{
			sq.Like{"LOWER(DisplayName)": sanitizedPrefix + "%"},
			sq.Like{"LOWER(Name)": sanitizedPrefix + "%"},
		}).
		Where(sq.Eq{"DeleteAt": 0}).
		OrderBy("DisplayName").
		Limit(10)

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build SQL query for searching groups")
	}

	var groups []*model.Group
	if err := ss.replica.Select(&groups, sql, args...); err != nil {
		return nil, errors.Wrap(err, "failed to search groups")
	}

	return groups, nil
}
