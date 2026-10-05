// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// mergeImageDefaults returns the config document with image_defaults merged
// under every entry of images; merged is false (and doc unusable) when there
// is no image_defaults to merge. The merge is done on the YAML tree rather than the decoded structs,
// so "the image sets this field" means the key is present — an image that
// writes `tags: []` keeps no tags rather than inheriting the defaults'.
func mergeImageDefaults(data []byte) (doc *yaml.Node, merged bool, err error) {
	doc = &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, false, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false, nil
	}
	root := deref(doc.Content[0])
	defaults := mappingValue(root, "image_defaults")
	images := mappingValue(root, "images")
	if defaults == nil || images == nil || images.Kind != yaml.SequenceNode || defaults.Tag == "!!null" {
		return nil, false, nil
	}
	if defaults.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("line %d: image_defaults must be a mapping", defaults.Line)
	}
	for i, img := range images.Content {
		img = deref(img)
		if img.Kind != yaml.MappingNode {
			return nil, false, fmt.Errorf("line %d: images[%d] must be a mapping", img.Line, i)
		}
		images.Content[i] = mergeMapping(defaults, img)
	}
	return doc, true, nil
}

// mergeMapping returns over laid on top of under: every key of over, plus the
// keys of under that over lacks. A key both have is over's, except that two
// mappings merge recursively.
func mergeMapping(under, over *yaml.Node) *yaml.Node {
	out := *over
	out.Content = append([]*yaml.Node(nil), over.Content...)
	for i := 0; i+1 < len(under.Content); i += 2 {
		key, uv := under.Content[i], deref(under.Content[i+1])
		j := keyIndex(&out, key.Value)
		if j < 0 {
			// A key the image takes from a YAML merge (<<: *anchor) is the
			// image's own, as far as the defaults are concerned.
			if !mergedKey(over, key.Value) {
				out.Content = append(out.Content, key, uv)
			}
			continue
		}
		if ov := deref(out.Content[j+1]); ov.Kind == yaml.MappingNode && uv.Kind == yaml.MappingNode {
			out.Content[j+1] = mergeMapping(uv, ov)
		}
	}
	return &out
}

// mergedKey reports whether mapping n gets key through a "<<" merge key.
func mergedKey(n *yaml.Node, key string) bool {
	src := mappingValue(n, "<<")
	if src == nil {
		return false
	}
	sources := []*yaml.Node{src}
	if src.Kind == yaml.SequenceNode {
		sources = src.Content
	}
	for _, s := range sources {
		if s = deref(s); keyIndex(s, key) >= 0 || mergedKey(s, key) {
			return true
		}
	}
	return false
}

// mappingValue returns the value under key in mapping n, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	if i := keyIndex(n, key); i >= 0 {
		return deref(n.Content[i+1])
	}
	return nil
}

// keyIndex is the index of key's key node in mapping n's Content, or -1.
func keyIndex(n *yaml.Node, key string) int {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// deref follows a YAML alias (*anchor) to the node it names.
func deref(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}
