package loghamster

import "os"

// InputFile is a file reader for files in the filesystem
type InputFile struct {
	Name  string // A logical name for a file (like authlog)
	Path  string
	Watch bool
	file  *os.File
}

// OutputFile is a file reader for files in the filesystem
type OutputFile struct {
	Name     string // A logical name for a file (like authlog)
	Path     string
	Compress bool
	file     *os.File
}

// NewInputFile will initialize a logfile input
func NewInputFile(path string, name string) *InputFile {
	return &InputFile{Name: name, Path: path}
}

// Open will open a logfile
func (f InputFile) Open() error {
	fh, err := os.Open(f.Path)
	f.file = fh
	return err
}

// FileManager holds all configured input and outputs
type FileManager struct {
	Inputs  []InputFile
	Outputs []OutputFile
}

// NewFileManager returns a stream manager to help finding
// inputs/outputs by rules
func NewFileManager() *FileManager {
	return &FileManager{}
}

// AddInput adds a new file input to the stream manager
func (mgr *FileManager) AddInput(input InputFile) {
	mgr.Inputs = append(mgr.Inputs, input)
}

// AddOutput adds a new file input to the stream manager
func (mgr *FileManager) AddOutput(output OutputFile) {
	mgr.Outputs = append(mgr.Outputs, output)
}

// FindInputByName will return an InputFile if found by name
// otherwise nil
func (mgr *FileManager) FindInputByName(name string) *InputFile {
	for _, file := range mgr.Inputs {
		if file.Name == name {
			return &file
		}
	}
	return nil
}

// FindInputByPath will return an InputFile if found by path
// otherwise nil
func (mgr *FileManager) FindInputByPath(path string) *InputFile {
	for _, file := range mgr.Inputs {
		if file.Path == path {
			return &file
		}
	}
	return nil
}

// FindOutputByName will return an InputFile if found by name
// otherwise nil
func (mgr *FileManager) FindOutputByName(name string) *OutputFile {
	for _, file := range mgr.Outputs {
		if file.Name == name {
			return &file
		}
	}
	return nil
}

// FindOutputByPath will return an OutputFile if found by path
// otherwise nil
func (mgr *FileManager) FindOutputByPath(path string) *OutputFile {
	for _, file := range mgr.Outputs {
		if file.Path == path {
			return &file
		}
	}
	return nil
}
