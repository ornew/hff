package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func relationItemType() hff.Type {
	return schema.Struct(schema.Int("price"), schema.Nullable(schema.Bool("active")), schema.List("tags", schema.StringType))
}
func relationEngine(t testing.TB) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", relationItemType()))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func relationBinding(t testing.TB, ordered bool) hffsql.Binding {
	return relationBindingWithTags(t, ordered, false, true)
}
func relationBindingWithTags(t testing.TB, ordered, nested, tagOrdered bool) hffsql.Binding {
	return relationBindingConfigured(t, ordered, nested, tagOrdered, false)
}
func relationBindingConfigured(t testing.TB, ordered, nested, tagOrdered, state bool) hffsql.Binding {
	t.Helper()
	tags := hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("related", "tags"), hffsql.JSON)
	if nested {
		options := []hffsql.RelationOption{hffsql.WithRelationSource(hffsql.Column("hff_tags"), "tagrow"), hffsql.WithCorrelation(hffsql.TrustedSQL("tagrow.item_id=related.id", hffsql.Column("related"), hffsql.Column("tagrow"))), hffsql.WithRelationItem(schema.StringType, hffsql.Bind(hff.Path{}, hffsql.Column("tagrow", "value")))}
		if tagOrdered {
			options = append(options, hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("tagrow", "position"), schema.IntType)))
		}
		relation, err := hffsql.NewRelation(options...)
		if err != nil {
			t.Fatal(err)
		}
		tags = hffsql.BindRelation(hff.Path{"tags"}, relation)
	}
	options := []hffsql.RelationOption{
		hffsql.WithRelationSource(hffsql.Column("hff_related"), "related"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("related.parent_id=records.id", hffsql.Column("records"), hffsql.Column("related"))),
		hffsql.WithRelationItem(relationItemType(), hffsql.BindStruct(hff.Path{}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"),
			hffsql.Bind(hff.Path{"price"}, hffsql.Column("related", "price")),
			hffsql.Bind(hff.Path{"active"}, hffsql.Column("related", "active")),
			tags)),
	}
	if state {
		options = append(options, hffsql.WithRelationState(hffsql.TrustedSQL("records.id<>0", hffsql.Column("records")), hffsql.TrustedSQL("records.id=1")))
	}
	if ordered {
		options = append(options, hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("related", "id"), schema.IntType)))
	}
	relation, err := hffsql.NewRelation(options...)
	if err != nil {
		t.Fatal(err)
	}
	return hffsql.BindRelation(hff.Path{"items"}, relation)
}
func relationUnorderedQueries() []string {
	return []string{
		`items`, `!items`, `items=null`, `items!=null`, `items:any(x=>x.price>10)`, `items:all(x=>x.active)`,
		`items:any(x=>x.price>10 x.active | x.tags:all(tag=>tag="auth"))`,
		`items=[]`, `items!=[]`, `items:size=0`, `items:size!=0`, `items:size>0`,
		`items:size<=0`, `!items:size=0`, `items:size>=2`,
		`items:exists`, `items:type="list"`, `items:unique`, `items:has(-1)`,
		`items:contains({"price":20,"active":true,"tags":["auth"]})`,
		`items!:any(x=>x.price>10)`,
	}
}
func relationOrderedQueries() []string {
	return []string{
		`items[0].price=20`, `items[-1].price=5`,
		`items:map(x=>x.price)=[20,5]`, `items:foldl(0,(acc,x)=>acc+x.price)=25`,
		`items:json:contains("price")`, `items=items`, `items!:json!=""`,
		`items:flatmap(x=>x.tags)=["auth","dev"]`,
		`items:foldl(0.0,(acc,x)=>{"keep":acc,"text":acc:string}.keep)=0.0`,
		`items:foldr(0.0,(x,acc)=>{"keep":acc,"text":acc:string}.keep)=0.0`,
		`items:map(x=>x.price):any(price=>price>10) items[0].price=20`,
		`items:concat(items):contains({"price":20,"active":true,"tags":["auth"]}) items[-1].price=5`,
	}
}

func TestRelationCanOrderByNativeDuration(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items[0]>0`)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("related_items"), "item"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("item.parent_id=records.id", hffsql.Column("item"), hffsql.Column("records"))),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("item", "price"))),
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("item", "elapsed_ns"), schema.DurationType)),
	)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), `"item"."elapsed_ns" ASC`) {
		t.Fatal("duration order column missing from SQL")
	}
}

func TestRelationBuildAndOrderingContract(t *testing.T) {
	e := relationEngine(t)
	for _, ordered := range []bool{false, true} {
		for _, source := range append(relationUnorderedQueries(), relationOrderedQueries()...) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBinding(t, ordered)))
			needsOrder := false
			for _, candidate := range relationOrderedQueries() {
				if candidate == source {
					needsOrder = true
				}
			}
			if !ordered && needsOrder {
				if err == nil {
					t.Fatal("unordered operation accepted", source)
				}
				continue
			}
			if err != nil {
				t.Fatal(source, err)
			}
			if !strings.Contains(q.Where(), "related.parent_id=records.id") {
				t.Fatal("correlation missing", source)
			}
		}
	}
}
func TestRelationRejectsIncompleteConfiguration(t *testing.T) {
	for _, options := range [][]hffsql.RelationOption{
		nil, {hffsql.RelationOption{}},
		{hffsql.WithRelationSource(hffsql.Column("table"), "row")},
		{hffsql.WithRelationSource(hffsql.Column("table"), "row"), hffsql.WithCorrelation(hffsql.TrustedSQL("true")), hffsql.WithRelationItem(schema.IntType, hffsql.Bind(hff.Path{"not-relative"}, hffsql.Column("row", "value")))},
	} {
		if _, err := hffsql.NewRelation(options...); err == nil {
			t.Fatal("incomplete relation accepted")
		}
	}
}

func TestNestedRelationRequiresOrderOnlyForDependentOperations(t *testing.T) {
	e := relationEngine(t)
	for _, source := range []string{`items:any(x=>x.tags:any(tag=>tag="auth"))`, `items:any(x=>x.tags[0]="auth")`, `items:map(x=>x.tags)=[["auth"]]`, `items:json!=""`, `items:unique`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBindingWithTags(t, true, true, false)))
		shouldPass := strings.Contains(source, "tags:any")
		if (err == nil) != shouldPass {
			t.Fatalf("%s: %v", source, err)
		}
	}
}

func TestIndependentRelationsCanReuseAliases(t *testing.T) {
	makeRelation := func(table string, typ hff.Type) hffsql.Relation {
		item := schema.Struct(schema.Field("value", typ))
		relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column(table), "item"), hffsql.WithCorrelation(hffsql.TrustedSQL("true")), hffsql.WithRelationItem(item, hffsql.BindStruct(hff.Path{}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), hffsql.Bind(hff.Path{"value"}, hffsql.Column("item", "value")))))
		if err != nil {
			t.Fatal(err)
		}
		return relation
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("numbers", schema.Struct(schema.Int("value"))), schema.List("strings", schema.Struct(schema.String("value"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `numbers:any(x=>x.value>1) strings:any(x=>x.value="a")`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"numbers"}, makeRelation("numbers", schema.IntType)), hffsql.BindRelation(hff.Path{"strings"}, makeRelation("strings", schema.StringType))))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRelationCallbacksPreserveFloatSerializationProvenance(t *testing.T) {
	item := schema.Struct(schema.Float("native"), schema.Float("lossy"))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("float_items"), "floatrow"), hffsql.WithCorrelation(hffsql.TrustedSQL("true")), hffsql.WithRelationItem(item, hffsql.BindStruct(hff.Path{}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), hffsql.Bind(hff.Path{"native"}, hffsql.Column("floatrow", "native")), hffsql.BindJSON(hff.Path{"lossy"}, hffsql.Column("floatrow", "lossy"), hffsql.JSONB))), hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("floatrow", "id"), schema.IntType)))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`items:foldl(0.0,(acc,x)=>{"keep":acc,"text":acc:string}.keep)>=0`, `items:foldr(0.0,(x,acc)=>{"keep":acc,"text":acc:string}.keep)>=0`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation))); err != nil {
			t.Fatal(source, err)
		}
	}
	for _, field := range []string{"native", "lossy"} {
		for _, source := range []string{
			`items:any(x=>x.FIELD:string="-0.0")`, `items!:any(x=>x.FIELD:string="-0.0")`,
			`items:all(x=>x.FIELD:json="-0.0")`, `items:map(x=>x.FIELD:string):contains("-0.0")`,
			`items:foldl("",(acc,x)=>acc:concat(x.FIELD:string))="-0.0"`,
			`items:foldr("",(x,acc)=>acc:concat(x.FIELD:string))="-0.0"`,
		} {
			source = strings.ReplaceAll(source, "FIELD", field)
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
			if (err != nil) != (field == "lossy") {
				t.Fatalf("%s: %v", source, err)
			}
		}
	}
}

func relationOrderBlindQueries() []string {
	return []string{
		`items:map(x=>x.price):any(price=>price>10)`, `items:map(x=>x.price):all(price=>price>10)`,
		`items:map(x=>x.price):size>=2`, `items:map(x=>x.price):has(-1)`,
		`items:map(x=>x.price):contains(20)`, `items:map(x=>x.price):unique`,
		`items:map(x=>x.price):sort=[5,20]`, `items:concat(items):any(x=>x.active)`,
		`items:concat(items):size>=4`, `items!:map(x=>x.price):any(price=>price>10)`,
		`items:map(x=>x.active!):any(v=>v)=null`, `!(items:map(x=>x.active!):any(v=>v)=null)`,
		`items:map(x=>x.active):contains(null)`,
		`items:map(x=>x.tags[0]):contains("auth")`,
	}
}
func TestRelationOrderIndependenceThroughTransforms(t *testing.T) {
	e := relationEngine(t)
	for _, source := range relationOrderBlindQueries() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBinding(t, false))); err != nil {
			t.Fatal(source, err)
		}
	}
	for _, source := range []string{`items:map(x=>x.price)[0]=20`, `items:concat(items)[0].price=20`, `items:flatmap(x=>x.tags):any(tag=>tag="auth")`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBinding(t, false))); err == nil {
			t.Fatal("order-dependent or unproven transform accepted", source)
		}
	}
}
func TestUnorderedFloatStableSortRetainsOrderRequirement(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("values", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("values_table"), "value_row"), hffsql.WithCorrelation(hffsql.TrustedSQL("true")), hffsql.WithRelationItem(schema.FloatType, hffsql.Bind(hff.Path{}, hffsql.Column("value_row", "value"))))
	if err != nil {
		t.Fatal(err)
	}
	insensitive, err := e.Compile(context.Background(), `values:sort:any(v=>v:string="-0.0")`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(context.Background(), insensitive, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"values"}, relation))); err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `values:sort[0]:string="-0.0"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"values"}, relation))); err == nil {
		t.Fatal("signed-zero tie ordering was discarded")
	}
}
