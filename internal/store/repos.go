package store

import "fmt"

// Repo is a row in the repos table (static registry)
type Repo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"default_branch"`
	MirrorPath    string `json:"mirror_path"`
	TokenRef      string `json:"token_ref"`
}

// Upsert inserts into a repo, or update it if no one with the same name exists.
// mirror_path is host-managed (set later...), so it's never overwritten here.
func (s *Store) UpsertRepo(r Repo) error {
	_, err := s.db.Exec(
		`
				INSERT into repos (name, github_owner, github_repo, default_branch, mirror_path, token_ref)
					VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT(name) DO UPDATE SET 
						github_owner = excluded.github_owner,
						github_repo = excluded.github_repo,
						default_branch = excluded.default_branch,
						token_ref = excluded.token_ref`,
		r.Name, r.Owner, r.Repo, r.DefaultBranch, r.MirrorPath, r.TokenRef,
	)
	if err != nil {
		return fmt.Errorf("upsert repo: %q: %w", r.Name, err)
	}
	return nil
}

// ListRepos returns all registered repos, ordered by name
func (s *Store) ListRepos() ([]Repo, error) {
	rows, err := s.db.Query(
		`SELECT id, name, github_owner, github_repo, default_branch, mirror_path, token_ref
			FROM repos
			ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query repos: %w", err)
	}
	defer rows.Close()

	var repos []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(
			&r.ID, &r.Name, &r.Owner, &r.Repo, &r.DefaultBranch, &r.MirrorPath, &r.TokenRef,
		); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repos: %w", err)
	}
	return repos, nil
}

// SetMirrorPath records where a repo's mirror cache lives. Deliberately
// separate from UpsertRepo: mirror_path is host state discovered at runtime,
// not config, so re-seeding the registry must never clobber it.
func (s *Store) SetMirrorPath(name, path string) error {
	res, err := s.db.Exec(`UPDATE repos set mirror_path = ? WHERE name = ?`, path, name)
	if err != nil {
		return fmt.Errorf("set mirror_path for %q: %w", name, err)
	}

	// An UPDATE that matches nothing is not an SQL error - it is a silent
	// no-op, so check explicitly rather than assume the repo existted.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no repo named %q", name)
	}

	return nil
}
