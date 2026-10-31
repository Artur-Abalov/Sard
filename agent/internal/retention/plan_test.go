// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/retention"
)

// The decision of a FORGET step, without a repository (docs/specs/agent/
// retention-forget.feature, L1a): what the policy, the parts, the hint about
// incomplete snapshots and the floor of the host leave of the snapshots.

// now is "сейчас" of the specification.
var now = time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC)

var (
	noFloor      = config.Floor{Disabled: true}
	builtInFloor = config.Floor{KeepLast: 7, KeepWithinDays: 14}
)

// id is a full snapshot id (64 hex characters) made from a short name.
func id(name string) string { return fmt.Sprintf("%-64s", name)[:64] }

func at(day int, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

// main is a main snapshot of source s.
func main(name, source string, t time.Time) restic.Snapshot {
	if source != "S" {
		name = source + name
	}
	return restic.Snapshot{ID: id(name), Time: t, Hostname: "maint", Paths: []string{"/data"},
		Tags: []string{"sard.source=" + source, "sard.run=r-" + name}}
}

// part is the fingerprint of the main snapshot.
func part(name, source, of string, t time.Time) restic.Snapshot {
	return restic.Snapshot{ID: id(name), Time: t.Add(time.Second), Hostname: "maint",
		Tags: []string{"sard.source=" + source, "files.part=fingerprint", "files.main_snapshot=" + id(of)}}
}

func globals(name, source, of string, t time.Time) restic.Snapshot {
	p := part(name, source, of, t)
	p.Tags = []string{"sard.source=" + source, "postgresql.part=globals", "postgresql.main_snapshot=" + id(of)}
	return p
}

// daily is one main snapshot of s at 10:00 of each of n days, the last today:
// m1 is the oldest.
func daily(s string, n int) []restic.Snapshot {
	var snaps []restic.Snapshot
	for i := 1; i <= n; i++ {
		snaps = append(snaps, main(fmt.Sprintf("m%d", i), s, time.Date(2026, 11, 30, 10, 0, 0, 0, time.UTC).AddDate(0, 0, i-n)))
	}
	return snaps
}

// weekly is one main snapshot of s at 10:00 of each of n weeks, the last today.
func weekly(s string, n int) []restic.Snapshot {
	var snaps []restic.Snapshot
	for i := 1; i <= n; i++ {
		snaps = append(snaps, main(fmt.Sprintf("m%d", i), s, time.Date(2026, 11, 30, 10, 0, 0, 0, time.UTC).AddDate(0, 0, 7*(i-n))))
	}
	return snaps
}

func ids(names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = id(n)
	}
	return out
}

func names(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("m%d", i))
	}
	return out
}

func sameSet(t *testing.T, what string, got []string, want []string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("%s = %d ids, want %d\n got  %q\n want %q", what, len(g), len(w), g, w)
	}
}

func plan(snaps []restic.Snapshot, task retention.Task, floor config.Floor) retention.Decision {
	if task.Source == "" {
		task.Source = "S"
	}
	return retention.Plan(snaps, task, floor, now)
}

func last(n uint32) retention.Policy { return retention.Policy{Last: n} }

func TestKeepLastОставляетПоследниеСнимкиИсточника(t *testing.T) {
	d := plan(daily("S", 5), retention.Task{Policy: last(2)}, noFloor)
	sameSet(t, "forget", d.Forget(), ids("m1", "m2", "m3"))
}

func TestСнимкиДругогоИсточникаИСнимкиБезМетокНеТрогаются(t *testing.T) {
	snaps := append(daily("S", 5), daily("T", 5)...)
	snaps = append(snaps, restic.Snapshot{ID: id("x1"), Time: at(1, 1)}, restic.Snapshot{ID: id("x2"), Time: at(2, 1), Tags: []string{"other"}})
	d := plan(snaps, retention.Task{Policy: last(1)}, noFloor)
	// T has the same names as S: ids repeat, so count them.
	if len(d.Forget()) != 4 {
		t.Errorf("forget %d ids: %q", len(d.Forget()), d.Forget())
	}
	for _, f := range d.Forget() {
		if f == id("x1") || f == id("x2") {
			t.Errorf("a snapshot without a source is forgotten: %q", f)
		}
	}
}

func TestСнимкиИсточникаСРазнымиХостамиИПутямиСчитаютсяОднойГруппой(t *testing.T) {
	snaps := daily("S", 6)
	for i := range snaps {
		snaps[i].Hostname, snaps[i].Paths = []string{"db1", "db2"}[i%2], []string{"data-a", "data-b"}[i%2:][:1]
	}
	d := plan(snaps, retention.Task{Policy: last(2)}, noFloor)
	sameSet(t, "forget", d.Forget(), ids(names(1, 4)...))
}

func TestKeepWithinСчитаетсяОтСамогоНовогоСнимкаИсточникаАНеОтСейчас(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)),
		main("m2", "S", time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)),
	}
	d := plan(snaps, retention.Task{Policy: retention.Policy{Within: 48 * time.Hour}}, noFloor)
	if len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestСнимокИзKeepSnapshotIdsОстаётсяСверхПолитики(t *testing.T) {
	d := plan(daily("S", 10), retention.Task{Policy: last(2), Keep: ids("m3")}, noFloor)
	sameSet(t, "forget", d.Forget(), ids("m1", "m2", "m4", "m5", "m6", "m7", "m8"))
}

func TestЧужойИлиНесуществующийIdВKeepSnapshotIdsНичегоНеМеняет(t *testing.T) {
	snaps := append(daily("S", 5), main("t1", "T", at(1, 1)))
	d := plan(snaps, retention.Task{Policy: last(2), Keep: ids("t1", "ffffffff")}, noFloor)
	sameSet(t, "forget", d.Forget(), ids("m1", "m2", "m3"))
}

func TestЧастиНеВходятВСчётПолитики(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", at(29, 10)), part("f1", "S", "m1", at(29, 10)),
		main("m2", "S", at(30, 10)), part("f2", "S", "m2", at(30, 10)),
	}
	if d := plan(snaps, retention.Task{Policy: last(2)}, noFloor); len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestОтпечатокЗабываетсяВместеСОсновнымСнимкомАГлобальныеСоСвоим(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", at(28, 10)), part("f1", "S", "m1", at(28, 10)),
		main("m2", "S", at(29, 10)), globals("g2", "S", "m2", at(29, 10)),
		main("m3", "S", at(30, 10)), part("f3", "S", "m3", at(30, 10)),
	}
	d := plan(snaps, retention.Task{Policy: last(1)}, noFloor)
	sameSet(t, "forget", d.Forget(), ids("m1", "f1", "m2", "g2"))
}

func TestЧастьОставленногоСнимкаОстаётсяДажеЕслиСтаршеГраницыПолитики(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)), part("f1", "S", "m1", time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)),
		main("m2", "S", time.Date(2026, 11, 30, 10, 0, 0, 0, time.UTC)),
	}
	snaps[1].Time = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	d := plan(snaps, retention.Task{Policy: retention.Policy{Within: 720 * time.Hour}}, noFloor)
	if len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestЧастьОсновногоСнимкаКоторогоНетЗабываетсяЛюбымШагомИсточника(t *testing.T) {
	snaps := append(daily("S", 3), part("f0", "S", "gone", at(1, 10)))
	d := plan(snaps, retention.Task{Policy: last(10)}, noFloor)
	sameSet(t, "forget", d.Forget(), ids("f0"))
}

func TestЧастиСтоятВСпискеРаньшеОсновныхСнимков(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", at(28, 10)), part("f1", "S", "m1", at(28, 10)),
		main("m2", "S", at(29, 10)), part("f2", "S", "m2", at(29, 10)),
		main("m3", "S", at(30, 10)),
	}
	d := plan(snaps, retention.Task{Policy: last(1)}, noFloor)
	sameSet(t, "parts", d.Parts, ids("f1", "f2"))
	sameSet(t, "main snapshots", d.Mains, ids("m1", "m2"))
}

const week = 168 * time.Hour

func incomplete(max time.Duration, names ...string) retention.Task {
	return retention.Task{Incomplete: ids(names...), IncompleteMaxAge: max, Policy: last(5)}
}

func TestНеполныйСнимокНеЗанимаетМестоПолногоВKeepDaily(t *testing.T) {
	snaps := []restic.Snapshot{main("m1", "S", at(30, 3)), main("n1", "S", at(30, 9))}
	task := incomplete(week, "n1")
	task.Policy = retention.Policy{Daily: 1}
	if d := plan(snaps, task, noFloor); len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestНеполныйСнимокСтаршеСрокаЗабываетсяЕслиЕстьПолныйНовее(t *testing.T) {
	snaps := []restic.Snapshot{main("n1", "S", at(20, 10)), main("m2", "S", at(29, 10))}
	d := plan(snaps, incomplete(week, "n1"), noFloor)
	sameSet(t, "forget", d.Forget(), ids("n1"))
}

func TestНеполныйСнимокСтаршеСрокаОстаётсяЕслиПолногоНовееНет(t *testing.T) {
	snaps := []restic.Snapshot{main("m1", "S", at(1, 10)), main("n2", "S", at(20, 10))}
	if d := plan(snaps, incomplete(week, "n2"), noFloor); len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestНеполныйСнимокМоложеСрокаОстаётся(t *testing.T) {
	snaps := []restic.Snapshot{main("n1", "S", at(28, 10)), main("m2", "S", at(29, 10))}
	if d := plan(snaps, incomplete(week, "n1"), noFloor); len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestНеполныйСнимокЗабываетсяВместеСоСвоимиЧастями(t *testing.T) {
	snaps := []restic.Snapshot{main("n1", "S", at(20, 10)), part("f1", "S", "n1", at(20, 10)), main("m2", "S", at(29, 10))}
	d := plan(snaps, incomplete(week, "n1"), noFloor)
	sameSet(t, "forget", d.Forget(), ids("n1", "f1"))
}

func TestПределСохраняетПоследние7СнимковПриПолитикеKeepLast1(t *testing.T) {
	d := plan(weekly("S", 20), retention.Task{Policy: last(1)}, builtInFloor)
	sameSet(t, "forget", d.Forget(), ids(names(1, 13)...))
	sameSet(t, "kept by floor", d.KeptByFloor, ids(names(14, 19)...))
}

func TestПределСохраняетВсеСнимкиЗа14ДнейОтСамогоНового(t *testing.T) {
	var snaps []restic.Snapshot
	for i := 0; i < 120; i++ { // 4 a day for 30 days; i = 0 is the oldest
		snaps = append(snaps, main(fmt.Sprintf("m%d", i), "S", at(30, 6).Add(-time.Duration(119-i)*6*time.Hour)))
	}
	d := plan(snaps, retention.Task{Policy: last(1)}, builtInFloor)
	cut := at(30, 6).Add(-14 * 24 * time.Hour)
	var want []string
	for _, s := range snaps {
		if !s.Time.After(cut) {
			want = append(want, s.ID)
		}
	}
	sameSet(t, "forget", d.Forget(), want)
}

func TestПределСчитаетсяДляКаждогоИсточникаОтдельно(t *testing.T) {
	floor := config.Floor{KeepLast: 3, KeepWithinDays: 1}
	snaps := append(weekly("S", 10), weekly("T", 10)...)
	d := plan(snaps, retention.Task{Policy: last(1)}, floor)
	if len(d.Forget()) != 7 {
		t.Errorf("forget %d ids: %q", len(d.Forget()), d.Forget())
	}
}

func TestПределЗащищаетЧастиСохранённыхИмОсновныхСнимков(t *testing.T) {
	var snaps []restic.Snapshot
	for _, m := range weekly("S", 10) {
		name := strings.TrimSpace(m.ID)
		snaps = append(snaps, m, part("f"+name, "S", name, m.Time))
	}
	d := plan(snaps, retention.Task{Policy: last(1)}, builtInFloor)
	for _, m := range snaps[len(snaps)-14:] {
		if slices.Contains(d.Forget(), m.ID) {
			t.Errorf("%s of a kept snapshot is forgotten", m.ID[:3])
		}
	}
}

func TestПределНеСлушаетПодсказкуСервераОНеполноте(t *testing.T) {
	task := incomplete(time.Second, names(1, 10)...)
	task.Policy = last(1)
	if d := plan(daily("S", 10), task, builtInFloor); len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
}

func TestПределЗащищаетНеполныйСнимокДажеЕслиПолныйНовее(t *testing.T) {
	snaps := []restic.Snapshot{main("n1", "S", at(20, 10)), main("m2", "S", at(29, 10))}
	d := plan(snaps, incomplete(week, "n1"), builtInFloor)
	if len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
	sameSet(t, "kept by floor", d.KeptByFloor, ids("n1"))
}

func TestПределСлабееПолитикиНичегоНеВычёркивает(t *testing.T) {
	d := plan(daily("S", 30), retention.Task{Policy: retention.Policy{Daily: 20}}, builtInFloor)
	sameSet(t, "forget", d.Forget(), ids(names(1, 10)...))
	if len(d.KeptByFloor) != 0 {
		t.Errorf("kept by floor = %q", d.KeptByFloor)
	}
}

func TestВыключенныйПределНичегоНеСохраняетСверхПолитики(t *testing.T) {
	d := plan(daily("S", 10), retention.Task{Policy: last(1)}, noFloor)
	sameSet(t, "forget", d.Forget(), ids(names(1, 9)...))
	if len(d.KeptByFloor) != 0 {
		t.Errorf("kept by floor = %q", d.KeptByFloor)
	}
}

func TestСтоШаговПодрядНеУходятГлубжеПредела(t *testing.T) {
	snaps := weekly("S", 20)
	for step := 1; step <= 100; step++ {
		d := plan(snaps, retention.Task{Policy: last(1)}, builtInFloor)
		if step > 1 && len(d.Forget()) != 0 {
			t.Fatalf("step %d forgets %q", step, d.Forget())
		}
		snaps = slices.DeleteFunc(snaps, func(s restic.Snapshot) bool { return slices.Contains(d.Forget(), s.ID) })
	}
	if len(snaps) != 7 {
		t.Errorf("%d snapshots left", len(snaps))
	}
}

func TestЗаданиеСИсточникомДругогоИсточникаНеВыходитЗаЕгоСнимки(t *testing.T) {
	snaps := append(weekly("S", 5), weekly("T", 20)...)
	d := plan(snaps, retention.Task{Source: "T", Policy: last(1)}, builtInFloor)
	if len(d.Forget()) != 13 {
		t.Errorf("forget %d ids", len(d.Forget()))
	}
}

// A snapshot dated in the future (a skewed or forged clock) must not move the
// boundary of the floor past the recent snapshots (restic does the same).
func TestСнимокИзБудущегоНеСдвигаетГраницуПредела(t *testing.T) {
	snaps := weekly("S", 10)
	snaps = append(snaps, main("future", "S", now.AddDate(10, 0, 0)))
	d := plan(snaps, retention.Task{Policy: last(1)}, config.Floor{KeepWithinDays: 14})
	// m10 (today) and m9 (7 days ago) are within 14 days of now.
	sameSet(t, "forget", d.Forget(), ids(names(1, 8)...))
}

// keep_within is exclusive, as in restic: exactly 14 days before the newest
// snapshot is not protected, one second less is.
func TestГраницаПределаВключаетСнимокМоложе14ДнейНоНеРовно14(t *testing.T) {
	snaps := []restic.Snapshot{
		main("new", "S", now),
		main("edge", "S", now.AddDate(0, 0, -14)),
		main("inside", "S", now.AddDate(0, 0, -14).Add(time.Second)),
	}
	d := plan(snaps, retention.Task{Policy: last(1)}, config.Floor{KeepWithinDays: 14})
	sameSet(t, "forget", d.Forget(), ids("edge"))
	sameSet(t, "kept by floor", d.KeptByFloor, ids("inside"))
}

func TestСнимокСДатойВБудущемНеСдвигаетГраницуПределаСКеепLast1(t *testing.T) {
	snaps := append(daily("S", 10), main("x1", "S", time.Date(2027, 6, 1, 10, 0, 0, 0, time.UTC)))
	d := plan(snaps, retention.Task{Policy: last(1)}, config.Floor{KeepLast: 1, KeepWithinDays: 14})
	if len(d.Forget()) != 0 {
		t.Errorf("forget = %q", d.Forget())
	}
	sameSet(t, "kept by floor", d.KeptByFloor, ids(names(1, 10)...))
}
