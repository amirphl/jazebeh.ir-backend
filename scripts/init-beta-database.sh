#!/bin/bash

# Database Initialization Script for Yamata no Orochi Beta
# This script creates the database and applies all migrations

set -Eeuo pipefail
set +x # Database environment values may contain credentials.
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
ASSUME_YES=false
for argument in "$@"; do
    case "$argument" in
        --yes|-y) ASSUME_YES=true ;;
        --help|-h)
            printf 'Usage: %s [--yes]\n' "$0"
            printf 'Apply pending beta database migrations after confirming all writers are stopped.\n'
            exit 0
            ;;
        *) printf 'Unknown option: %s\n' "$argument" >&2; exit 2 ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

# Load environment variables from .env.beta file if it exists
if [ -f "$PROJECT_ROOT/.env.beta" ]; then
    [ ! -L "$PROJECT_ROOT/.env.beta" ] || { print_error "Refusing symlinked .env.beta"; exit 1; }
    chmod 600 "$PROJECT_ROOT/.env.beta"
    print_status "Loading environment variables from .env.beta file..."
    # Treat dotenv content as data; never execute it as shell code.
    # shellcheck disable=SC1091
    source "$SCRIPT_DIR/load-yamata-env.sh"
    load_yamata_env_file "$PROJECT_ROOT/.env.beta"
fi

if docker info >/dev/null 2>&1; then
    DOCKER=(docker)
elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
    DOCKER=(sudo docker)
else
    print_error "Docker is unavailable or requires an interactive sudo login"
    exit 1
fi
readonly DOCKER

read_migration_tracker() {
    local tracking_file="$PROJECT_ROOT/.migration_tracker_beta"
    local value=""
    if [ ! -e "$tracking_file" ]; then
        printf '%s' ""
        return 0
    fi
    [ ! -L "$tracking_file" ] || { print_error "Refusing symlinked migration tracker" >&2; return 1; }
    [ -f "$tracking_file" ] || { print_error "Migration tracker is not a regular file" >&2; return 1; }
    IFS= read -r value < "$tracking_file" || true
    [[ "$value" =~ ^[0-9]{4}_[A-Za-z0-9_]+\.sql$ ]] || {
        print_error "Migration tracker is empty or malformed" >&2
        return 1
    }
    [ "$(awk 'END { print NR }' "$tracking_file")" -eq 1 ] || {
        print_error "Migration tracker must contain exactly one filename" >&2
        return 1
    }
    printf '%s' "$value"
}

write_migration_tracker() {
    local filename="$1"
    local temporary
    temporary=$(mktemp "$PROJECT_ROOT/.migration_tracker_beta.XXXXXX")
    printf '%s\n' "$filename" > "$temporary"
    chmod 600 "$temporary"
    mv -f -- "$temporary" "$PROJECT_ROOT/.migration_tracker_beta"
}

# Function to get database configuration from environment variables
get_db_config() {
    # Database configuration with defaults
    DB_HOST="172.30.0.10"
    DB_PORT=${DB_PORT:-5432}
    DB_NAME=${DB_NAME:-yamata_no_orochi}
    DB_USER=${DB_USER:-yamata_user}
    [[ "$DB_PORT" =~ ^[0-9]+$ ]] && ((10#$DB_PORT >= 1 && 10#$DB_PORT <= 65535)) || {
        print_error "DB_PORT must be between 1 and 65535"
        return 1
    }
    [[ "$DB_NAME" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
        print_error "DB_NAME must be a simple PostgreSQL identifier"
        return 1
    }
    [[ "$DB_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || {
        print_error "DB_USER must be a simple PostgreSQL identifier"
        return 1
    }
    
    # Export for use in functions
    export DB_HOST DB_PORT DB_NAME DB_USER
    
    print_status "Database Configuration:"
    echo "  Host: $DB_HOST"
    echo "  Port: $DB_PORT"
    echo "  Database: $DB_NAME"
    echo "  User: $DB_USER"
    echo ""
}

# Function to check if PostgreSQL container is running
check_postgres_container() {
    if ! "${DOCKER[@]}" ps --format "{{.Names}}" | grep -Fxq "yamata-postgres-beta"; then
        print_error "PostgreSQL container is not running. Please start the services first:"
        echo "  docker-compose -f docker-compose.beta.yml up -d postgres-beta"
        exit 1
    fi
    
    # Wait for PostgreSQL to be ready
    print_status "Waiting for PostgreSQL to be ready..."
    local max_attempts=30
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if "${DOCKER[@]}" exec yamata-postgres-beta pg_isready -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; then
            print_success "PostgreSQL is ready!"
            return 0
        fi
        
        echo "Attempt $attempt/$max_attempts - Waiting for PostgreSQL..."
        sleep 2
        attempt=$((attempt + 1))
    done
    
    print_error "PostgreSQL failed to start within expected time"
    return 1
}

check_application_writers_stopped() {
    for container in yamata-app-beta yamata-campaign-scheduler-beta; do
        if "${DOCKER[@]}" container inspect "$container" >/dev/null 2>&1 &&
            [ "$("${DOCKER[@]}" inspect -f '{{.State.Running}}' "$container")" = true ]; then
            print_error "Stop $container before applying migrations"
            return 1
        fi
    done
}

# Function to check if database exists
check_database_exists() {
    local result

    # psql does not perform variable interpolation on SQL passed with -c. Feed
    # the query over stdin so :'db_name' is safely quoted by psql instead of
    # reaching PostgreSQL as invalid syntax. Treat query failures separately
    # from a successful query that returns no row.
    if ! result=$(
        printf '%s\n' "SELECT 1 FROM pg_database WHERE datname = :'db_name';" |
            "${DOCKER[@]}" exec -i yamata-postgres-beta \
                psql -X -v ON_ERROR_STOP=1 -v db_name="$DB_NAME" -U "$DB_USER" -d postgres -tA
    ); then
        print_error "Failed to query PostgreSQL for database '$DB_NAME'"
        return 2
    fi

    if [ "$result" = "1" ]; then
        print_status "Database '$DB_NAME' already exists"
        return 0
    fi

    print_status "Database '$DB_NAME' does not exist"
    return 1
}

# Function to apply migrations
apply_migrations() {
    print_status "Applying database migrations..."
    
    # Migration tracking file
    local tracking_file="$PROJECT_ROOT/.migration_tracker_beta"
    local last_migration=""
    
    # Read last applied migration if tracking file exists
    if [ -e "$tracking_file" ]; then
        last_migration=$(read_migration_tracker) || return 1
        print_status "Last applied migration: $last_migration"
        print_status "Will resume from the next migration after this one"
    else
        # A missing filesystem tracker is ambiguous on a populated database:
        # replaying historical migrations may duplicate data or apply a
        # destructive historical transformation.  A genuinely new database
        # has no application tables yet (extensions/functions are fine).
        local public_tables
        public_tables=$("${DOCKER[@]}" exec yamata-postgres-beta \
            psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" -Atc \
            "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p');")
        [[ "$public_tables" == 0 ]] || {
            print_error "Migration tracker is missing but database contains $public_tables public tables"
            print_error "Refusing to replay historical migrations against an unknown schema state"
            return 1
        }
    fi
    
    # Get all migration files
    local migration_dir="$PROJECT_ROOT/migrations"
    local migration_files=()
    
    # Find all .sql files that are not down migrations and not run_all_up.sql
    while IFS= read -r -d '' file; do
        local filename=$(basename "$file")
        # Skip down migrations and run_all_up.sql
        if [[ "$filename" != *"_down.sql" && "$filename" != "run_all_up.sql" ]]; then
            migration_files+=("$file")
        fi
    done < <(find "$migration_dir" -name "*.sql" -type f -print0 | sort -z)
    
    if [ ${#migration_files[@]} -eq 0 ]; then
        print_warning "No migration files found in $migration_dir"
        return 0
    fi
    
    print_status "Found ${#migration_files[@]} migration files (excluding run_all_up.sql)"
    
    # Filter migrations to apply only those after the last applied one
    local migrations_to_apply=()
    local found_last=false
    
    if [ -z "$last_migration" ]; then
        # First run, apply all migrations
        migrations_to_apply=("${migration_files[@]}")
        print_status "First run - will apply all migrations"
    else
        # Find migrations after the last applied one
        for file in "${migration_files[@]}"; do
            local filename=$(basename "$file")
            if [ "$found_last" = true ]; then
                migrations_to_apply+=("$file")
            elif [ "$filename" = "$last_migration" ]; then
                found_last=true
                print_status "Found last applied migration: $filename"
            fi
        done

        if [ "$found_last" != true ]; then
            print_error "Migration tracker references an unavailable migration: $last_migration"
            print_error "Refusing to guess database migration state"
            return 1
        fi
        
        if [ ${#migrations_to_apply[@]} -eq 0 ]; then
            print_success "All migrations are already applied (last: $last_migration)"
            return 0
        fi
        
        print_status "Found ${#migrations_to_apply[@]} new migrations to apply"
    fi
    
    # Apply each migration
    for file in "${migrations_to_apply[@]}"; do
        local filename=$(basename "$file")
        print_status "Applying migration: $filename"
        
        if "${DOCKER[@]}" exec -i yamata-postgres-beta psql -X -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" < "$file"; then
            print_success "Migration '$filename' applied successfully"
            
            # Update tracking file with the last applied migration
            write_migration_tracker "$filename"
            print_status "Updated migration tracker: $filename"
        else
            print_error "Failed to apply migration '$filename'"
            return 1
        fi
    done
    
    print_success "All pending migrations applied successfully"
    return 0
}

# Function to verify database setup
verify_database_setup() {
    print_status "Verifying database setup..."
    
    # Check if tables exist
    local tables=(
        "account_types" "customers" "src_reference"
        "bundle_audience_selection_members"
        "campaign_targeting_capacity_calculations"
    )
    local missing_tables=()
    
    for table in "${tables[@]}"; do
        if "${DOCKER[@]}" exec yamata-postgres-beta psql -X -U "$DB_USER" -d "$DB_NAME" -tAc "SELECT 1 FROM information_schema.tables WHERE table_name='$table'" | grep -q 1; then
            print_success "Table '$table' exists"
        else
            print_warning "Table '$table' is missing"
            missing_tables+=("$table")
        fi
    done
    
    if [ ${#missing_tables[@]} -gt 0 ]; then
        print_error "Missing tables: ${missing_tables[*]}"
        return 1
    else
        print_success "All expected tables exist"
    fi
    
    # Check if audit_action_enum exists
    if "${DOCKER[@]}" exec yamata-postgres-beta psql -X -U "$DB_USER" -d "$DB_NAME" -tAc "SELECT 1 FROM pg_type WHERE typname='audit_action_enum'" | grep -q 1; then
        print_success "audit_action_enum type exists"
    else
        print_error "audit_action_enum type is missing"
        return 1
    fi
}

# Function to show database information
show_database_info() {
    print_status "Database Information:"
    echo "  Host: $DB_HOST"
    echo "  Port: $DB_PORT"
    echo "  Database: $DB_NAME"
    echo "  User: $DB_USER"
    echo ""
    
    # Show table count
    local table_count
    table_count=$("${DOCKER[@]}" exec yamata-postgres-beta psql -X -U "$DB_USER" -d "$DB_NAME" -tAc "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public'")
    echo "  Tables: $table_count"
    
    # Exact row counts can require full-table scans and do not establish schema
    # readiness. Keep routine deployments limited to catalog-level checks.
}

# Function to show migration status
show_migration_status() {
    local tracking_file="$PROJECT_ROOT/.migration_tracker_beta"
    local migration_dir="$PROJECT_ROOT/migrations"
    
    print_status "Migration Status:"
    
    if [ -e "$tracking_file" ]; then
        local last_migration
        last_migration=$(read_migration_tracker) || return 1
        echo "  Last applied migration: $last_migration"
    else
        echo "  No migration tracking file found (first run)"
    fi
    
    # Count total available migrations
    local total_migrations=0
    local applied_migrations=0
    
    while IFS= read -r -d '' file; do
        local filename=$(basename "$file")
        if [[ "$filename" != *"_down.sql" && "$filename" != "run_all_up.sql" ]]; then
            total_migrations=$((total_migrations + 1))
            
            # Check if this migration is already applied
            if [ -e "$tracking_file" ]; then
                local last_applied
                last_applied=$(read_migration_tracker) || return 1
                if [ "$filename" = "$last_applied" ] || [ "$filename" \< "$last_applied" ]; then
                    applied_migrations=$((applied_migrations + 1))
                fi
            fi
        fi
    done < <(find "$migration_dir" -name "*.sql" -type f -print0 | sort -z)
    
    echo "  Total migrations available: $total_migrations"
    echo "  Migrations already applied: $applied_migrations"
    echo "  Pending migrations: $((total_migrations - applied_migrations))"
    echo ""
}

# Main function
main() {
    echo "🗄️  Yamata no Orochi - Beta Database Initialization"
    echo "=================================================="
    echo ""
    
    # Get database configuration
    get_db_config
    
    # Check if PostgreSQL container is running
    check_postgres_container
    check_application_writers_stopped
    
    # Check if database exists — never create it; a missing DB is a fatal error
    local database_check_status=0
    check_database_exists || database_check_status=$?
    case "$database_check_status" in
        0) ;;
        1)
            print_error "Database '$DB_NAME' does not exist. Create it manually before running this script."
            exit 1
            ;;
        *)
            print_error "Could not determine whether database '$DB_NAME' exists; no migrations were run."
            exit "$database_check_status"
            ;;
    esac
    
    # Show migration status before asking for confirmation
    show_migration_status
    
    # Apply migrations
    print_status "Ready to apply database migrations."
    if [ "$ASSUME_YES" = true ]; then
        REPLY=y
    else
        read -r -p "Do you want to proceed with applying migrations? [y/N]: " -n 1 REPLY
        echo
    fi
    if [[ ${REPLY:-} =~ ^[Yy]$ ]]; then
        apply_migrations
    else
        print_error "Migration step cancelled; database was not initialized"
        return 2
    fi
    
    # Verify database setup
    verify_database_setup
    
    # Show database information
    show_database_info
    
    print_success "🎉 Beta database initialization completed successfully!"
    echo ""
    echo "📋 Next Steps:"
    echo "  1. Your application should now be able to connect to the database"
    echo "  2. Check the application logs for any connection issues"
    echo "  3. Test the API endpoints to ensure everything is working"
    echo ""
}

# Run main function
main
