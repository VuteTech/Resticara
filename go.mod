module resticara

go 1.21

require (
	github.com/yourusername/resticara/emailsender v0.0.0-00010101000000-000000000000
	gopkg.in/ini.v1 v1.67.0
)

require github.com/stretchr/testify v1.8.4 // indirect

replace github.com/yourusername/resticara/emailsender => ./emailsender
