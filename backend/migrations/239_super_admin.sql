-- Introduce the super_admin role and guarantee a live instance keeps an elevated administrator.
-- Existing installs promote the earliest active admin only when no active super_admin exists.
UPDATE users
SET role = 'super_admin', updated_at = NOW()
WHERE id = (
    SELECT id
    FROM users
    WHERE role = 'admin'
      AND status = 'active'
      AND deleted_at IS NULL
      AND NOT EXISTS (
          SELECT 1
          FROM users
          WHERE role = 'super_admin'
            AND status = 'active'
            AND deleted_at IS NULL
      )
    ORDER BY created_at ASC, id ASC
    LIMIT 1
);