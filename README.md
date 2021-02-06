shrimp is a cli tool that can upload large files to Amazon S3.

The primary purpose of this tool is to be more convenient than the official aws cli by providing a way to easily resume interrupted multipart uploads.

Current status: **under development**. Please do not use it for important files just yet. Please report bugs.

TODO:
- Make interactive (keyboard input to pause, change bandwidth limit, etc).
- Make flags compatible with the aws cli.
- Upload multiple parts in parallel.
- Resume upload with gaps.
