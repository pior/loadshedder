module example-gin

go 1.24

replace (
	github.com/pior/loadshedder => ../..
	github.com/pior/loadshedder/ginware => ../../ginware
)

require (
	github.com/gin-gonic/gin v1.10.0
	github.com/pior/loadshedder v0.0.0
	github.com/pior/loadshedder/ginware v0.0.0
)
