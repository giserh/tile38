package server

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/geojson"
	"github.com/tidwall/gjson"
	"github.com/tidwall/resp"
	"github.com/tidwall/sjson"
	"github.com/tidwall/tile38/core"
	"github.com/tidwall/tile38/internal/collection"
)

func appendJSONString(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] == '\\' || s[i] == '"' || s[i] > 126 {
			d, _ := json.Marshal(s)
			return append(b, string(d)...)
		}
	}
	b = append(b, '"')
	b = append(b, s...)
	b = append(b, '"')
	return b
}

func jsonString(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] == '\\' || s[i] == '"' || s[i] > 126 {
			d, _ := json.Marshal(s)
			return string(d)
		}
	}
	b := make([]byte, len(s)+2)
	b[0] = '"'
	copy(b[1:], s)
	b[len(b)-1] = '"'
	return string(b)
}
func appendJSONSimpleBounds(dst []byte, o geojson.Object) []byte {
	bbox := o.Rect()
	dst = append(dst, `{"sw":{"lat":`...)
	dst = strconv.AppendFloat(dst, bbox.Min.Y, 'f', -1, 64)
	dst = append(dst, `,"lon":`...)
	dst = strconv.AppendFloat(dst, bbox.Min.X, 'f', -1, 64)
	dst = append(dst, `},"ne":{"lat":`...)
	dst = strconv.AppendFloat(dst, bbox.Max.Y, 'f', -1, 64)
	dst = append(dst, `,"lon":`...)
	dst = strconv.AppendFloat(dst, bbox.Max.X, 'f', -1, 64)
	dst = append(dst, `}}`...)
	return dst
}

func appendJSONSimplePoint(dst []byte, o geojson.Object) []byte {
	point := o.Center()
	z, _ := geojson.IsPoint(o)
	dst = append(dst, `{"lat":`...)
	dst = strconv.AppendFloat(dst, point.Y, 'f', -1, 64)
	dst = append(dst, `,"lon":`...)
	dst = strconv.AppendFloat(dst, point.X, 'f', -1, 64)
	if z != 0 {
		dst = append(dst, `,"z":`...)
		dst = strconv.AppendFloat(dst, z, 'f', -1, 64)
	}
	dst = append(dst, '}')
	return dst
}

func appendJSONTimeFormat(b []byte, t time.Time) []byte {
	b = append(b, '"')
	b = t.AppendFormat(b, "2006-01-02T15:04:05.999999999Z07:00")
	b = append(b, '"')
	return b
}

func jsonTimeFormat(t time.Time) string {
	var b []byte
	b = appendJSONTimeFormat(b, t)
	return string(b)
}

func (c *Server) cmdJget(msg *Message) (resp.Value, error) {
	start := time.Now()

	if len(msg.Args) < 3 {
		return NOMessage, errInvalidNumberOfArguments
	}
	if len(msg.Args) > 5 {
		return NOMessage, errInvalidNumberOfArguments
	}
	key := msg.Args[1]
	id := msg.Args[2]
	var doget bool
	var path string
	var raw bool
	if len(msg.Args) > 3 {
		doget = true
		path = msg.Args[3]
		if len(msg.Args) == 5 {
			if strings.ToLower(msg.Args[4]) == "raw" {
				raw = true
			} else {
				return NOMessage, errInvalidArgument(msg.Args[4])
			}
		}
	}
	col := c.getCol(key)
	if col == nil {
		if msg.OutputType == RESP {
			return resp.NullValue(), nil
		}
		return NOMessage, errKeyNotFound
	}
	o, _, ok := col.Get(id)
	if !ok {
		if msg.OutputType == RESP {
			return resp.NullValue(), nil
		}
		return NOMessage, errIDNotFound
	}
	var res gjson.Result
	if doget {
		res = gjson.Get(o.String(), path)
	} else {
		res = gjson.Parse(o.String())
	}
	var val string
	if raw {
		val = res.Raw
	} else {
		val = res.String()
	}
	var buf bytes.Buffer
	if msg.OutputType == JSON {
		buf.WriteString(`{"ok":true`)
	}
	switch msg.OutputType {
	case JSON:
		if res.Exists() {
			buf.WriteString(`,"value":` + jsonString(val))
		}
		buf.WriteString(`,"elapsed":"` + time.Now().Sub(start).String() + "\"}")
		return resp.StringValue(buf.String()), nil
	case RESP:
		if !res.Exists() {
			return resp.NullValue(), nil
		}
		return resp.StringValue(val), nil
	}
	return NOMessage, nil
}

func (c *Server) cmdJset(msg *Message) (res resp.Value, d commandDetails, err error) {
	// JSET key path value [RAW]
	start := time.Now()

	var raw, str bool
	switch len(msg.Args) {
	default:
		return NOMessage, d, errInvalidNumberOfArguments
	case 5:
	case 6:
		switch strings.ToLower(msg.Args[5]) {
		default:
			return NOMessage, d, errInvalidArgument(msg.Args[5])
		case "raw":
			raw = true
		case "str":
			str = true
		}
	}

	key := msg.Args[1]
	id := msg.Args[2]
	path := msg.Args[3]
	val := msg.Args[4]
	if !str && !raw {
		switch val {
		default:
			if len(val) > 0 {
				if (val[0] >= '0' && val[0] <= '9') || val[0] == '-' {
					if _, err := strconv.ParseFloat(val, 64); err == nil {
						raw = true
					}
				}
			}
		case "true", "false", "null":
			raw = true
		}
	}
	col := c.getCol(key)
	var createcol bool
	if col == nil {
		col = collection.New(core.PackedFields)
		createcol = true
	}
	var json string
	var geoobj bool
	o, _, ok := col.Get(id)
	if ok {
		geoobj = objIsSpatial(o)
		json = o.String()
	}
	if raw {
		// set as raw block
		json, err = sjson.SetRaw(json, path, val)
	} else {
		// set as a string
		json, err = sjson.Set(json, path, val)
	}
	if err != nil {
		return NOMessage, d, err
	}

	if geoobj {
		nmsg := *msg
		nmsg.Args = []string{"SET", key, id, "OBJECT", json}
		// SET key id OBJECT json
		return c.cmdSet(&nmsg)
	}
	if createcol {
		c.setCol(key, col)
	}

	d.key = key
	d.id = id
	d.obj = collection.String(json)
	d.timestamp = time.Now()
	d.updated = true

	c.clearIDExpires(key, id)
	col.Set(d.id, d.obj, nil, nil)
	switch msg.OutputType {
	case JSON:
		var buf bytes.Buffer
		buf.WriteString(`{"ok":true`)
		buf.WriteString(`,"elapsed":"` + time.Now().Sub(start).String() + "\"}")
		return resp.StringValue(buf.String()), d, nil
	case RESP:
		return resp.SimpleStringValue("OK"), d, nil
	}
	return NOMessage, d, nil
}

func (c *Server) cmdJdel(msg *Message) (res resp.Value, d commandDetails, err error) {
	start := time.Now()

	if len(msg.Args) != 4 {
		return NOMessage, d, errInvalidNumberOfArguments
	}
	key := msg.Args[1]
	id := msg.Args[2]
	path := msg.Args[3]

	col := c.getCol(key)
	if col == nil {
		if msg.OutputType == RESP {
			return resp.IntegerValue(0), d, nil
		}
		return NOMessage, d, errKeyNotFound
	}

	var json string
	var geoobj bool
	o, _, ok := col.Get(id)
	if ok {
		geoobj = objIsSpatial(o)
		json = o.String()
	}
	njson, err := sjson.Delete(json, path)
	if err != nil {
		return NOMessage, d, err
	}
	if njson == json {
		switch msg.OutputType {
		case JSON:
			return NOMessage, d, errPathNotFound
		case RESP:
			return resp.IntegerValue(0), d, nil
		}
		return NOMessage, d, nil
	}
	json = njson
	if geoobj {
		nmsg := *msg
		nmsg.Args = []string{"SET", key, id, "OBJECT", json}
		// SET key id OBJECT json
		return c.cmdSet(&nmsg)
	}

	d.key = key
	d.id = id
	d.obj = collection.String(json)
	d.timestamp = time.Now()
	d.updated = true

	c.clearIDExpires(d.key, d.id)
	col.Set(d.id, d.obj, nil, nil)
	switch msg.OutputType {
	case JSON:
		var buf bytes.Buffer
		buf.WriteString(`{"ok":true`)
		buf.WriteString(`,"elapsed":"` + time.Now().Sub(start).String() + "\"}")
		return resp.StringValue(buf.String()), d, nil
	case RESP:
		return resp.IntegerValue(1), d, nil
	}
	return NOMessage, d, nil
}
