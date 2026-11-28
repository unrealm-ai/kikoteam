package service

import (
	"bytes"
	"testing"
)

func TestGeneratedTaskSpaceImageStaysWithItsTask(t *testing.T) {
	root := t.TempDir()
	workspace, employee, space, task, id := NewID(), NewID(), NewID(), NewID(), NewID()
	data := []byte("fixed original")
	name := "outputs/images/" + id + ".png"
	if err := MaterializeImage(root, workspace, employee, "", space, task, id, data); err != nil {
		t.Fatal(err)
	}
	b, err := ReadWorkFile(root, workspace, employee, "", space, task, name)
	if err != nil || !bytes.Equal(b, data) {
		t.Fatal(err)
	}
	if _, err = ReadWorkFile(root, workspace, employee, "", space, NewID(), name); err == nil {
		t.Fatal("another task could read the copy")
	}
	if err = MaterializeImage(root, workspace, employee, "", space, task, id, []byte("different")); err == nil {
		t.Fatal("immutable copy overwritten")
	}
}
