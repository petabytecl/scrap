BEGIN {
	FS = ":.*##"
	name_width = 42
	printf "\n\033[1mUsage:\033[0m\n  make \033[36m<target>\033[0m\n"
}

/^##@/ {
	printf "\n\033[1m%s\033[0m\n", substr($0, 5)
	next
}

/^##\?/ {
	line = substr($0, 5)
	name = line
	description = ""
	if (match(line, /[[:space:]]/)) {
		name = substr(line, 1, RSTART - 1)
		description = substr(line, RSTART + RLENGTH)
	}
	printf "  \033[36m%-*s\033[0m %s\n", name_width, name, description
	next
}

/^[a-zA-Z0-9_.-]+:.*##/ {
	printf "  \033[36m%-*s\033[0m %s\n", name_width, $1, $2
}
