// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

import type { ComponentType } from 'react';
import {
  ApiOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  BellOutlined,
  BugOutlined,
  CloudOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  FilterOutlined,
  FolderOutlined,
  GlobalOutlined,
  HeartOutlined,
  KeyOutlined,
  LineChartOutlined,
  LinkOutlined,
  LockOutlined,
  MailOutlined,
  MessageOutlined,
  PieChartOutlined,
  RocketOutlined,
  SafetyOutlined,
  SettingOutlined,
  StarOutlined,
  TagsOutlined,
  TeamOutlined,
  ThunderboltOutlined,
  ToolOutlined,
  UserOutlined,
  WarningOutlined,
} from '@ant-design/icons';

// A plugin names an icon with a plain word; only these exist, anything else is the generic one.
const ICONS: Record<string, ComponentType> = {
  api: ApiOutlined,
  bell: BellOutlined,
  'bar-chart': BarChartOutlined,
  bug: BugOutlined,
  chart: LineChartOutlined,
  cloud: CloudOutlined,
  'cloud-server': CloudServerOutlined,
  code: CodeOutlined,
  dashboard: DashboardOutlined,
  database: DatabaseOutlined,
  experiment: ExperimentOutlined,
  file: FileTextOutlined,
  filter: FilterOutlined,
  folder: FolderOutlined,
  global: GlobalOutlined,
  heart: HeartOutlined,
  key: KeyOutlined,
  'line-chart': LineChartOutlined,
  link: LinkOutlined,
  lock: LockOutlined,
  mail: MailOutlined,
  message: MessageOutlined,
  notification: BellOutlined,
  'pie-chart': PieChartOutlined,
  rocket: RocketOutlined,
  safety: SafetyOutlined,
  setting: SettingOutlined,
  star: StarOutlined,
  tags: TagsOutlined,
  team: TeamOutlined,
  thunderbolt: ThunderboltOutlined,
  tool: ToolOutlined,
  user: UserOutlined,
  warning: WarningOutlined,
};

export function pluginIcon(name: string): ComponentType {
  return ICONS[name] ?? AppstoreOutlined;
}
