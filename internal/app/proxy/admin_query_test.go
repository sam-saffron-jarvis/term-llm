package proxy

import (
	"strings"
	"testing"
)

func TestCallerLastUsedIsIndexedLatestAdmission(t *testing.T) {
	s := testService(t)
	if _, err := s.store.db.Exec(`INSERT INTO callers(id,name,hash,created) VALUES('busy','Busy','busy-hash',1),('unused','Unused','unused-hash',2)`); err != nil {
		t.Fatal(err)
	}
	// Retained history with a backwards wall-clock jump: sequence defines latest admission.
	if _, err := s.store.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<2048)
 INSERT INTO requests(id,started,caller,route,method,path,outcome,data)
 SELECT 'r-'||x,4096-x,'busy','route','POST','/opaque','complete','{}' FROM n`); err != nil {
		t.Fatal(err)
	}
	callers, err := s.store.callers()
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 2 || callers[0].LastUsed != 2048 || callers[1].LastUsed != 0 {
		t.Fatalf("latest admission and unused caller: %+v", callers)
	}
	rows, err := s.store.db.Query("EXPLAIN QUERY PLAN " + callersQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "requests_caller") {
			indexed = true
		}
		if strings.Contains(detail, "SCAN r") {
			t.Fatalf("unbounded request-history scan: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("last-used lookup must use requests_caller index")
	}
}
