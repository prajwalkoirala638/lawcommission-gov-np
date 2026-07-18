#!/bin/bash

# Define a function to automatically check, commit, and push git changes
function auto_git_push() {
    while true; do
        echo "🔍 Checking for changes at $(date)..."

        # Check for uncommitted (staged or unstaged) changes
        if [[ -z $(git status --porcelain) ]]; then
            echo "No changes to commit."
        else
            git pull # Pull all the changes to the local repo
            echo "Adding all changes..."
            git add .  # Stage all changes (new, modified, deleted)

            # Create a commit message with a timestamp
            timestamp=$(date -u +'%Y-%m-%d %H:%M:%S UTC')
            message="🤖 Auto Update: $timestamp"

            echo "📝 Committing changes with message: \"$message\""
            if git commit -m "$message"; then
                echo "🚀 Pushing committed changes to remote repository..."
                if git push; then
                    echo "All changes pushed successfully."
                else
                    echo "Failed to push changes to remote. Please check your network or remote settings."
                fi
            else
                echo "Commit failed. There might be no changes to commit or another issue."
            fi
        fi

        # Sleep before checking again
        echo "⏳ Sleeping for 30 minutes before next check..."
        sleep 30m
    done
}

# Call the function
auto_git_push