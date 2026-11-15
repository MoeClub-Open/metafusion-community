-- Allow favoriting top-level event entities alongside the existing eight entity kinds.
ALTER TABLE community.favorites DROP CONSTRAINT IF EXISTS favorites_target_type_check;
ALTER TABLE community.favorites ADD CONSTRAINT favorites_target_type_check
  CHECK (target_type IN ('agent','collection','work','content_unit','expression','release','medium','track','event'));
