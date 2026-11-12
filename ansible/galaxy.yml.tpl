# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

name: hrc
namespace: dimidiumlabs

version: '@VERSION@'
authors: [Nikolay Govorov]
license: [BSD-3-Clause, MIT]
repository: https://github.com/mrdimidium/hrc

readme: README.md
description: Install and remove Hosted Runner Controller
documentation: https://dimidiumlabs.io/hrc/doc/@VERSION@/manual/installation.html

dependencies:
  ansible.windows: '>=3.0.0'
build_ignore: [galaxy.yml.tpl]
