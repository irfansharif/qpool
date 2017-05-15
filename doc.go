// Copyright 2017, Irfan Sharif
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Irfan Sharif (irfanmahmoudsharif@gmail.com)

// Package qpool is an efficient implementation of a quota pool designed for
// concurrent use. Quota pools, including the one here, can be used for flow
// control or admission control purposes. This implementation however differs
// in that it allows for arbitrary quota acquisitions thus allowing for finer
// grained resource management.
// Additionally for blocking calls qpool allows for asynchronous context
// cancellations by internally composing locks with channels.
package qpool
