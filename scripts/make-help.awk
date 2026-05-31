BEGIN {
	FS = ":.*##"
	name_width = 42
	mode = ENVIRON["MAKE_HELP_MODE"]
	if (mode == "") {
		mode = "targets"
	}
	show_targets = mode == "targets" || mode == "all"
	show_variables = mode == "variables" || mode == "all"
	if (show_targets) {
		printf "\n\033[1mUsage:\033[0m\n  make \033[36m<target>\033[0m\n"
	} else {
		printf "\n\033[1mOverridable Make Variables:\033[0m\n"
	}
}

/^##@/ {
	title = substr($0, 5)
	is_variable_section = title ~ /Variables$/
	if (is_variable_section && !show_variables) {
		next
	}
	if (!is_variable_section && !show_targets) {
		next
	}
	printf "\n\033[1m%s\033[0m\n", title
	next
}

/^##\?/ {
	if (!show_variables) {
		next
	}
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
	if (!show_targets) {
		next
	}
	printf "  \033[36m%-*s\033[0m %s\n", name_width, $1, $2
}
