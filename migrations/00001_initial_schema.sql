-- +goose Up
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE "role" (
    "id" integer NOT NULL,
    "name" character varying NOT NULL,
    CONSTRAINT "PK_b36bcfe02fc8de3c57a8b2391c2" PRIMARY KEY ("id")
);

CREATE TABLE "status" (
    "id" integer NOT NULL,
    "name" character varying NOT NULL,
    CONSTRAINT "PK_e12743a7086ec826733f54e1d95" PRIMARY KEY ("id")
);

CREATE TABLE "file" (
    "id" uuid NOT NULL DEFAULT uuid_generate_v4(),
    "path" character varying NOT NULL,
    "visibility" character varying(10) NOT NULL DEFAULT 'PUBLIC',
    "ownerUserId" integer,
    "mimeType" character varying(100),
    "size" integer,
    "sha256" character varying(64),
    CONSTRAINT "PK_36b46d232307066b3a2c9ea3a1d" PRIMARY KEY ("id")
);
CREATE INDEX "IDX_file_ownerUserId" ON "file" ("ownerUserId");
CREATE INDEX "IDX_file_path" ON "file" ("path");
CREATE INDEX "IDX_file_sha256" ON "file" ("sha256");

CREATE TABLE "user" (
    "id" serial NOT NULL,
    "email" character varying,
    "password" character varying,
    "provider" character varying NOT NULL DEFAULT 'email',
    "firstName" character varying,
    "lastName" character varying,
    "phone" character varying,
    "nickname" character varying,
    "gender" integer DEFAULT 0,
    "birthday" date,
    "wechatOpenId" character varying,
    "wechatUnionId" character varying,
    "alipayOpenId" character varying,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    "deletedAt" timestamp without time zone,
    "photoId" uuid,
    "roleId" integer,
    "statusId" integer,
    CONSTRAINT "PK_cace4a159ff9f2512dd42373760" PRIMARY KEY ("id"),
    CONSTRAINT "UQ_e12875dfb3b1d92d7d7c5377e22" UNIQUE ("email"),
    CONSTRAINT "user_phone_key" UNIQUE ("phone"),
    CONSTRAINT "user_wechatOpenId_key" UNIQUE ("wechatOpenId"),
    CONSTRAINT "user_alipayOpenId_key" UNIQUE ("alipayOpenId"),
    CONSTRAINT "REL_75e2be4ce11d447ef43be0e374" UNIQUE ("photoId"),
    CONSTRAINT "FK_75e2be4ce11d447ef43be0e374f" FOREIGN KEY ("photoId") REFERENCES "file" ("id"),
    CONSTRAINT "FK_c28e52f758e7bbc53828db92194" FOREIGN KEY ("roleId") REFERENCES "role" ("id"),
    CONSTRAINT "FK_dc18daa696860586ba4667a9d31" FOREIGN KEY ("statusId") REFERENCES "status" ("id")
);
CREATE INDEX "IDX_58e4dbff0e1a32a9bdc861bb29" ON "user" ("firstName");
CREATE INDEX "IDX_f0e1b4ecdca13b177e2e3a0613" ON "user" ("lastName");
CREATE INDEX "IDX_user_deletedAt" ON "user" ("deletedAt") WHERE "deletedAt" IS NULL;
CREATE INDEX "IDX_user_phone" ON "user" ("phone");
CREATE INDEX "IDX_user_wechatOpenId" ON "user" ("wechatOpenId");
CREATE INDEX "IDX_user_wechatUnionId" ON "user" ("wechatUnionId");

CREATE TABLE "session" (
    "id" serial NOT NULL,
    "hash" character varying NOT NULL,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    "deletedAt" timestamp without time zone,
    "userId" integer,
    "userAgent" character varying(512),
    "ip" character varying(64),
    "lastActiveAt" timestamp without time zone,
    CONSTRAINT "PK_f55da76ac1c3ac420f444d2ff11" PRIMARY KEY ("id"),
    CONSTRAINT "FK_3d2f174ef04fb312fdebd0ddc53" FOREIGN KEY ("userId") REFERENCES "user" ("id")
);
CREATE INDEX "IDX_3d2f174ef04fb312fdebd0ddc5" ON "session" ("userId");
CREATE INDEX "IDX_session_deletedAt" ON "session" ("deletedAt") WHERE "deletedAt" IS NULL;

CREATE TABLE "permission" (
    "id" character varying(50) NOT NULL,
    "name" character varying(100) NOT NULL,
    "description" character varying(255),
    "resource" character varying(50) NOT NULL,
    "action" character varying(50) NOT NULL,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_permission_id" PRIMARY KEY ("id")
);
CREATE INDEX "IDX_PERMISSION_RESOURCE" ON "permission" ("resource");

CREATE TABLE "role_permission" (
    "role_id" integer NOT NULL,
    "permission_id" character varying(50) NOT NULL,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_role_permission" PRIMARY KEY ("role_id", "permission_id"),
    CONSTRAINT "FK_ROLE_PERMISSION_ROLE" FOREIGN KEY ("role_id") REFERENCES "role" ("id") ON DELETE CASCADE,
    CONSTRAINT "FK_ROLE_PERMISSION_PERMISSION" FOREIGN KEY ("permission_id") REFERENCES "permission" ("id") ON DELETE CASCADE
);
CREATE INDEX "IDX_ROLE_PERMISSION_PERMISSION" ON "role_permission" ("permission_id");
CREATE INDEX "IDX_ROLE_PERMISSION_ROLE" ON "role_permission" ("role_id");

CREATE TABLE "audit_logs" (
    "id" serial NOT NULL,
    "userId" character varying,
    "action" character varying(50) NOT NULL,
    "entityType" character varying(100) NOT NULL,
    "entityId" character varying NOT NULL,
    "oldValue" jsonb,
    "newValue" jsonb,
    "ipAddress" character varying,
    "userAgent" text,
    "requestId" character varying,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_audit_logs" PRIMARY KEY ("id")
);
CREATE INDEX "IDX_audit_logs_action" ON "audit_logs" ("action");
CREATE INDEX "IDX_audit_logs_createdAt" ON "audit_logs" ("createdAt");
CREATE INDEX "IDX_audit_logs_entityId" ON "audit_logs" ("entityId");
CREATE INDEX "IDX_audit_logs_entityType" ON "audit_logs" ("entityType");
CREATE INDEX "IDX_audit_logs_requestId" ON "audit_logs" ("requestId");
CREATE INDEX "IDX_audit_logs_userId" ON "audit_logs" ("userId");

CREATE TABLE "config" (
    "key" character varying(200) NOT NULL,
    "value" jsonb NOT NULL,
    "isPublic" boolean NOT NULL DEFAULT false,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_config_key" PRIMARY KEY ("key")
);

CREATE TABLE "region" (
    "id" serial NOT NULL,
    "code" character varying(12) NOT NULL,
    "name" character varying(50) NOT NULL,
    "level" integer NOT NULL,
    "parentCode" character varying(12),
    "sort" integer NOT NULL DEFAULT 0,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_region_id" PRIMARY KEY ("id"),
    CONSTRAINT "UQ_region_code" UNIQUE ("code")
);
COMMENT ON TABLE "region" IS 'Administrative division data (Province/City/District)';
CREATE INDEX "IDX_region_code" ON "region" ("code");
CREATE INDEX "IDX_region_level" ON "region" ("level");
CREATE INDEX "IDX_region_level_parentCode" ON "region" ("level", "parentCode");
CREATE INDEX "IDX_region_parentCode" ON "region" ("parentCode");

CREATE TABLE "notification" (
    "id" serial NOT NULL,
    "userId" integer NOT NULL,
    "type" character varying(20) NOT NULL,
    "category" character varying(20) NOT NULL,
    "title" character varying(200) NOT NULL,
    "content" text NOT NULL,
    "metadata" jsonb,
    "channels" jsonb NOT NULL DEFAULT '[]',
    "sentChannels" jsonb NOT NULL DEFAULT '{}',
    "isRead" boolean NOT NULL DEFAULT false,
    "readAt" timestamp without time zone,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    "deletedAt" timestamp without time zone,
    CONSTRAINT "PK_notification_id" PRIMARY KEY ("id"),
    CONSTRAINT "FK_notification_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);
CREATE INDEX "IDX_notification_category" ON "notification" ("category");
CREATE INDEX "IDX_notification_createdAt" ON "notification" ("createdAt");
CREATE INDEX "IDX_notification_isRead" ON "notification" ("isRead");
CREATE INDEX "IDX_notification_userId" ON "notification" ("userId");

CREATE TABLE "notification_setting" (
    "id" serial NOT NULL,
    "userId" integer NOT NULL,
    "category" character varying(20) NOT NULL,
    "enableInApp" boolean NOT NULL DEFAULT true,
    "enableEmail" boolean NOT NULL DEFAULT true,
    "enableSms" boolean NOT NULL DEFAULT true,
    "enablePush" boolean NOT NULL DEFAULT true,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_notification_setting_id" PRIMARY KEY ("id"),
    CONSTRAINT "UQ_notification_setting_userId_category" UNIQUE ("userId", "category"),
    CONSTRAINT "FK_notification_setting_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);
CREATE INDEX "IDX_notification_setting_userId" ON "notification_setting" ("userId");

CREATE TABLE "address" (
    "id" serial NOT NULL,
    "userId" integer NOT NULL,
    "name" character varying(50) NOT NULL,
    "phone" character varying(20) NOT NULL,
    "province" character varying(50) NOT NULL,
    "city" character varying(50) NOT NULL,
    "district" character varying(50) NOT NULL,
    "provinceCode" character varying(12),
    "cityCode" character varying(12),
    "districtCode" character varying(12),
    "address" character varying(200) NOT NULL,
    "isDefault" boolean NOT NULL DEFAULT false,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    "deletedAt" timestamp without time zone,
    CONSTRAINT "PK_address_id" PRIMARY KEY ("id"),
    CONSTRAINT "FK_address_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);
COMMENT ON COLUMN "address"."provinceCode" IS 'Province region code';
COMMENT ON COLUMN "address"."cityCode" IS 'City region code';
COMMENT ON COLUMN "address"."districtCode" IS 'District region code';
CREATE INDEX "IDX_address_cityCode" ON "address" ("cityCode");
CREATE INDEX "IDX_address_deletedAt" ON "address" ("deletedAt") WHERE "deletedAt" IS NULL;
CREATE INDEX "IDX_address_districtCode" ON "address" ("districtCode");
CREATE INDEX "IDX_address_provinceCode" ON "address" ("provinceCode");
CREATE INDEX "IDX_address_userId" ON "address" ("userId");
CREATE INDEX "IDX_address_userId_isDefault_createdAt" ON "address" ("userId", "isDefault" DESC, "createdAt" DESC);
CREATE UNIQUE INDEX "UQ_address_userId_default" ON "address" ("userId") WHERE "isDefault" AND "deletedAt" IS NULL;

CREATE TABLE "favorite" (
    "id" serial NOT NULL,
    "userId" integer NOT NULL,
    "targetType" character varying(20) NOT NULL,
    "targetId" character varying(100) NOT NULL,
    "subjectId" uuid,
    "title" character varying(200),
    "image" character varying(500),
    "extra" text,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_favorite_id" PRIMARY KEY ("id"),
    CONSTRAINT "UQ_favorite_user_target" UNIQUE ("userId", "targetType", "targetId"),
    CONSTRAINT "FK_favorite_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);
CREATE INDEX "IDX_favorite_targetId" ON "favorite" ("targetId");
CREATE INDEX "IDX_favorite_targetType" ON "favorite" ("targetType");
CREATE INDEX "IDX_favorite_userId" ON "favorite" ("userId");
CREATE INDEX "IDX_favorite_userId_subjectId" ON "favorite" ("userId", "subjectId");
CREATE INDEX "IDX_favorite_userId_targetType" ON "favorite" ("userId", "targetType");

CREATE TABLE "feedback" (
    "id" serial NOT NULL,
    "userId" integer,
    "type" character varying(20) NOT NULL,
    "content" text NOT NULL,
    "images" jsonb,
    "contact" character varying(100),
    "status" character varying(20) NOT NULL DEFAULT 'pending',
    "reply" text,
    "questionId" uuid,
    "questionVersion" integer,
    "reason" character varying(20),
    "errataId" uuid,
    "messageId" uuid,
    "messageSnapshot" jsonb,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_feedback_id" PRIMARY KEY ("id"),
    CONSTRAINT "FK_feedback_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE SET NULL
);
CREATE INDEX "IDX_feedback_errataId" ON "feedback" ("errataId");
CREATE INDEX "IDX_feedback_questionId" ON "feedback" ("questionId");
CREATE INDEX "IDX_feedback_status" ON "feedback" ("status");
CREATE INDEX "IDX_feedback_type" ON "feedback" ("type");
CREATE INDEX "IDX_feedback_userId" ON "feedback" ("userId");

CREATE TABLE "user_consent" (
    "id" uuid NOT NULL DEFAULT uuid_generate_v4(),
    "userId" integer NOT NULL,
    "type" character varying(20) NOT NULL,
    "version" character varying(50) NOT NULL,
    "agreedAt" timestamp without time zone NOT NULL DEFAULT now(),
    "ip" character varying(45),
    CONSTRAINT "PK_user_consent" PRIMARY KEY ("id"),
    CONSTRAINT "FK_user_consent_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);
CREATE UNIQUE INDEX "UQ_user_consent_userId_type_version" ON "user_consent" ("userId", "type", "version");

CREATE TABLE "account_cleanup" (
    "userId" integer NOT NULL,
    "completedAt" timestamp with time zone NOT NULL,
    CONSTRAINT "PK_account_cleanup" PRIMARY KEY ("userId"),
    CONSTRAINT "FK_account_cleanup_user" FOREIGN KEY ("userId") REFERENCES "user" ("id") ON DELETE CASCADE
);

CREATE TABLE "admin_alert" (
    "id" serial NOT NULL,
    "type" character varying(50) NOT NULL,
    "level" character varying(20) NOT NULL,
    "message" text NOT NULL,
    "payload" jsonb,
    "dedupKey" character varying(200) NOT NULL,
    "acknowledgedAt" timestamp without time zone,
    "createdAt" timestamp without time zone NOT NULL DEFAULT now(),
    "updatedAt" timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT "PK_44cf0b17edf69fb3f9b62c1ee7b" PRIMARY KEY ("id")
);
CREATE INDEX "IDX_admin_alert_createdAt" ON "admin_alert" ("createdAt");
CREATE INDEX "IDX_admin_alert_type" ON "admin_alert" ("type");
CREATE UNIQUE INDEX "UQ_admin_alert_dedupKey_open" ON "admin_alert" ("dedupKey") WHERE "acknowledgedAt" IS NULL;

-- +goose Down
DROP TABLE "admin_alert";
DROP TABLE "account_cleanup";
DROP TABLE "user_consent";
DROP TABLE "feedback";
DROP TABLE "favorite";
DROP TABLE "address";
DROP TABLE "notification_setting";
DROP TABLE "notification";
DROP TABLE "region";
DROP TABLE "config";
DROP TABLE "audit_logs";
DROP TABLE "role_permission";
DROP TABLE "permission";
DROP TABLE "session";
DROP TABLE "user";
DROP TABLE "file";
DROP TABLE "status";
DROP TABLE "role";
