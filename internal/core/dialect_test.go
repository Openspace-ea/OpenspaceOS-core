package core

import "testing"

func TestRebindPostgres(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "no params",
			sql:  "SELECT * FROM nodes",
			want: "SELECT * FROM nodes",
		},
		{
			name: "single",
			sql:  "SELECT * FROM nodes WHERE node_id = ?",
			want: "SELECT * FROM nodes WHERE node_id = $1",
		},
		{
			name: "multiple sequential",
			sql:  "DELETE FROM relationships WHERE from_node_id = ? OR to_node_id = ?",
			want: "DELETE FROM relationships WHERE from_node_id = $1 OR to_node_id = $2",
		},
		{
			name: "insert then limit",
			sql:  "SELECT id FROM nodes WHERE node_type = ? ORDER BY created_at LIMIT ? OFFSET ?",
			want: "SELECT id FROM nodes WHERE node_type = $1 ORDER BY created_at LIMIT $2 OFFSET $3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rebindPostgres(c.sql); got != c.want {
				t.Fatalf("rebindPostgres(%q) = %q, want %q", c.sql, got, c.want)
			}
		})
	}
}

func TestRebindIdentity(t *testing.T) {
	in := "DELETE FROM nodes WHERE node_id = ?"
	if got := rebindIdentity(in); got != in {
		t.Fatalf("rebindIdentity should return input unchanged, got %q", got)
	}
}