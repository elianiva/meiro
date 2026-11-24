package mcpcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
)

const ConfirmClear = "ask the user before clearing upcoming tracks, then pass confirm: true"

type schema map[string]any

func object(fields schema, required ...string) schema {
	if required == nil {
		required = []string{}
	}
	return schema{"type": "object", "properties": fields, "required": required, "additionalProperties": false}
}

func array(items schema) schema { return schema{"type": "array", "items": items} }

func numeric(kind string, minimum, maximum int) schema {
	return schema{"type": kind, "minimum": minimum, "maximum": maximum}
}

func choices(kind string, values ...any) schema { return schema{"type": kind, "enum": values} }

type definition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Input       schema      `json:"inputSchema"`
	Output      schema      `json:"outputSchema"`
	Annotations annotations `json:"annotations"`
}

type annotations struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

var (
	text    = schema{"type": "string"}
	boolean = schema{"type": "boolean"}
	index   = schema{"type": "integer", "minimum": 0}
	videoID = schema{"type": "string", "pattern": "^[A-Za-z0-9_-]{11}$"}
	track   = object(schema{"video_id": text, "collection_id": text, "subtitle": text, "title": text, "artists": text, "album": text, "duration": text, "kind": text, "rating": choices("string", "like", "dislike", "none", "unknown")},
		"video_id", "collection_id", "subtitle", "title", "artists", "album", "duration", "kind", "rating")
	state = object(schema{
		"track": track, "status": choices("string", "stopped", "loading", "playing", "paused"),
		"position_seconds": schema{"type": "number"}, "current_index": schema{"type": "integer", "minimum": -1},
		"queue_total": index, "shuffle": boolean, "repeat": choices("integer", 0, 1, 2),
		"volume": numeric("number", 0, 100), "seeking": boolean, "playback_error": text,
	}, "track", "status", "position_seconds", "current_index", "queue_total", "shuffle", "repeat", "volume", "seeking")
	items        = object(schema{"items": array(track)}, "items")
	rating       = choices("string", "like", "dislike", "none")
	collectionID = schema{"type": "string", "pattern": "^(MPR|VL|PL|UC)[A-Za-z0-9_-]{1,197}$"}
	playNow      = object(schema{"video_id": videoID, "collection_id": collectionID, "shuffle": boolean})
	page         = object(schema{"items": array(track), "total": index, "offset": index, "recommendations_start": schema{"type": "integer", "minimum": -1}, "current_index": schema{"type": "integer", "minimum": -1}},
		"items", "total", "offset", "current_index", "recommendations_start")

	tools = []definition{
		{"now_playing", "Read the current track and playback state. The track ID is empty when no track is loaded.", object(schema{}), state, annotations{ReadOnly: true, Idempotent: true}},
		{"queue_list", "Read the queue in playback order, including recommendations. recommendations_start is the zero-based start of the recommendation section, or -1 when absent. Offset defaults to 0 and limit to 20.", object(schema{"offset": index, "limit": numeric("integer", 1, 50)}), page, annotations{ReadOnly: true, Idempotent: true}},
		{"search", "Search YouTube Music for songs (default), videos, albums, playlists, artists, or all. Use video_id to play or queue tracks. Pass collection_id to play_now to play a collection.", object(schema{"query": schema{"type": "string", "minLength": 1, "maxLength": 200}, "kind": choices("string", "songs", "videos", "albums", "playlists", "artists", "all")}, "query"), items, annotations{ReadOnly: true, OpenWorld: true}},
		{"playback", "Play, pause, toggle playback, or go to the next or previous track. Returns the playback state.", object(schema{"action": choices("string", "play", "pause", "toggle", "next", "previous")}, "action"), state, annotations{OpenWorld: true}},
		{"play_now", "Use one video_id to insert a track next and start it, or one collection_id to replace the queue with an album, playlist, or artist tracks. Shuffle is available for collections and defaults to false.", playNow, state, annotations{Destructive: true, OpenWorld: true}},
		{"queue_add", "Add tracks in the given order. Use next to insert after the current track or end to append before upcoming recommendations. Adding to an empty queue starts playback.", object(schema{"video_ids": schema{"type": "array", "items": videoID, "minItems": 1, "maxItems": 10}, "position": choices("string", "next", "end")}, "video_ids", "position"), state, annotations{OpenWorld: true}},
		{"queue_remove", "Remove one entry by its queue index. The current track cannot be removed. Read the queue again before removing another entry.", object(schema{"index": index}, "index"), state, annotations{Destructive: true}},
		{"queue_move", "Move an entry to its final queue index. Read the queue again before another move.", object(schema{"index": index, "to": index}, "index", "to"), state, annotations{}},
		{"queue_clear", "Remove upcoming tracks and keep the current and played tracks. Requires confirm: true and an explicit request from the user.", object(schema{"confirm": schema{"type": "boolean", "const": true}}, "confirm"), state, annotations{Destructive: true, Idempotent: true}},
		{"seek", "Seek to an absolute position in seconds in the current track. Start playback first.", object(schema{"seconds": numeric("number", 0, 86400)}, "seconds"), state, annotations{Idempotent: true}},
		{"set_volume", "Set Meiro's volume from 0 to 100 and save it for the next launch.", object(schema{"volume": numeric("number", 0, 100)}, "volume"), state, annotations{Idempotent: true}},
		{"set_shuffle", "Enable or disable shuffle for the main queue. The recommendation section keeps its order.", object(schema{"enabled": boolean}, "enabled"), state, annotations{Idempotent: true}},
		{"set_repeat", "Set repeat to 0 (off), 1 (queue) or 2 (current track).", object(schema{"mode": choices("integer", 0, 1, 2)}, "mode"), state, annotations{Idempotent: true}},
		{"like", "Set a track rating to like, dislike or none. Check the track rating in now_playing or queue_list before writing. Defaults to the current track. Requires sign-in.", object(schema{"video_id": videoID, "rating": rating}, "rating"), object(schema{"video_id": videoID, "rating": rating}, "video_id", "rating"), annotations{Destructive: true, Idempotent: true, OpenWorld: true}},
		{"start_radio", "Replace the queue and start a radio from a track. Defaults to the current track.", object(schema{"video_id": videoID}), state, annotations{Destructive: true, OpenWorld: true}},
	}
)

func init() {
	playNow["oneOf"] = []schema{
		{"required": []string{"video_id"}, "not": schema{"anyOf": []schema{{"required": []string{"collection_id"}}, {"required": []string{"shuffle"}}}}},
		{"required": []string{"collection_id"}, "not": schema{"required": []string{"video_id"}}},
	}
}

func KnownTool(name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func DecodeArgs(name string, raw json.RawMessage, into any) error {
	for _, tool := range tools {
		if tool.Name == name {
			return decodeArgs(tool.Input, raw, into)
		}
	}
	return errors.New("unknown tool; list the Meiro tools and use a listed name")
}

func decodeArgs(input schema, raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var fields map[string]json.RawMessage
	if !decodeObject(raw, &fields) {
		return errors.New("arguments must be an object")
	}
	required, _ := input["required"].([]string)
	properties, _ := input["properties"].(schema)
	for _, key := range required {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			if key == "confirm" {
				return errors.New(ConfirmClear)
			}
			return fmt.Errorf("missing argument: %s", key)
		}
	}
	for key, value := range fields {
		if _, ok := properties[key]; !ok || string(value) == "null" {
			return fmt.Errorf("invalid argument: %s", key)
		}
	}
	if err := json.Unmarshal(raw, into); err != nil {
		var wrongType *json.UnmarshalTypeError
		if errors.As(err, &wrongType) && wrongType.Field != "" {
			return fmt.Errorf("invalid argument: %s", wrongType.Field)
		}
		return errors.New("invalid arguments")
	}
	return nil
}
