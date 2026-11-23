package service

func validAttachmentSelection(paths, ids []string, channelIDsAllowed bool) bool {
	if len(paths)+len(ids) > 32 {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		_, ordinal, valid := SplitInputAttachmentID(id)
		if !valid || seen[id] || ordinal >= 0 && !channelIDsAllowed {
			return false
		}
		seen[id] = true
	}
	return true
}

func resolveTaskAttachmentIDs(tx SessionTx, ids []string) ([]Attachment, error) {
	store, ok := tx.(interface {
		ResolveTaskAttachmentIDs([]string) ([]Attachment, error)
	})
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "附件授权不可用")
	}
	return store.ResolveTaskAttachmentIDs(ids)
}
